package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var (
	_ CloudConnectionStore = (*SQLiteStore)(nil)
	_ CloudConnectionStore = (*PostgresStore)(nil)
)

func lockCloudConnectionTx(tx *storeTx, id string) (bool, error) {
	result, err := tx.Exec(`UPDATE cloud_connections SET updated_at = updated_at WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

const cloudConnectionSelect = `SELECT id, name, COALESCE(description, ''), provider, auth_mode,
	COALESCE(azure_cloud, ''), COALESCE(azure_tenant_id, ''), COALESCE(azure_subscription_id, ''),
	COALESCE(azure_resource_group, ''), COALESCE(azure_client_id, ''),
	COALESCE(aws_partition, ''), COALESCE(aws_account_id, ''), COALESCE(aws_region, ''),
	COALESCE(aws_access_key_id, ''), COALESCE(aws_role_arn, ''),
	COALESCE(client_secret_ciphertext, ''), COALESCE(client_secret_wrapped_key, ''),
	COALESCE(aws_secret_key_ciphertext, ''), COALESCE(aws_secret_key_wrapped_key, ''),
	COALESCE(aws_external_id_ciphertext, ''), COALESCE(aws_external_id_wrapped_key, ''),
	COALESCE(credential_env_var, ''), COALESCE(status, 'active'), last_checked_at,
	COALESCE(last_check_status, ''), COALESCE(last_check_message, ''), COALESCE(labels_json, ''),
	created_at, updated_at FROM cloud_connections`

func scanCloudConnection(row capacityRowScanner) (*CloudConnection, error) {
	var connection CloudConnection
	var labelsJSON string
	var checked sql.NullTime
	if err := row.Scan(&connection.ID, &connection.Name, &connection.Description, &connection.Provider, &connection.AuthMode,
		&connection.AzureCloud, &connection.AzureTenantID, &connection.AzureSubscriptionID,
		&connection.AzureResourceGroup, &connection.AzureClientID,
		&connection.AWSPartition, &connection.AWSAccountID, &connection.AWSRegion,
		&connection.AWSAccessKeyID, &connection.AWSRoleARN,
		&connection.ClientSecretCiphertext, &connection.ClientSecretWrappedKey,
		&connection.AWSSecretKeyCiphertext, &connection.AWSSecretKeyWrappedKey,
		&connection.AWSExternalIDCiphertext, &connection.AWSExternalIDWrappedKey,
		&connection.CredentialEnvVar, &connection.Status, &checked,
		&connection.LastCheckStatus, &connection.LastCheckMessage, &labelsJSON,
		&connection.CreatedAt, &connection.UpdatedAt); err != nil {
		return nil, err
	}
	if checked.Valid {
		value := checked.Time
		connection.LastCheckedAt = &value
	}
	connection.Labels = unmarshalTags(labelsJSON)
	return &connection, nil
}

func (s *SQLiteStore) CreateCloudConnection(connection *CloudConnection) error {
	if connection == nil {
		return errors.New("cloud connection is required")
	}
	if err := connection.Validate(); err != nil {
		return err
	}
	now := localVersionTimestamp()
	if connection.CreatedAt.IsZero() {
		connection.CreatedAt = now
	}
	connection.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO cloud_connections (
		id, name, description, provider, auth_mode, azure_cloud, azure_tenant_id, azure_subscription_id,
		azure_resource_group, azure_client_id, aws_partition, aws_account_id, aws_region, aws_access_key_id,
		aws_role_arn, client_secret_ciphertext, client_secret_wrapped_key, aws_secret_key_ciphertext,
		aws_secret_key_wrapped_key, aws_external_id_ciphertext, aws_external_id_wrapped_key,
		credential_env_var, status, labels_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		connection.ID, connection.Name, connection.Description, connection.Provider, connection.AuthMode,
		connection.AzureCloud, connection.AzureTenantID, connection.AzureSubscriptionID,
		connection.AzureResourceGroup, connection.AzureClientID, connection.AWSPartition,
		connection.AWSAccountID, connection.AWSRegion, connection.AWSAccessKeyID, connection.AWSRoleARN,
		connection.ClientSecretCiphertext, connection.ClientSecretWrappedKey,
		connection.AWSSecretKeyCiphertext, connection.AWSSecretKeyWrappedKey,
		connection.AWSExternalIDCiphertext, connection.AWSExternalIDWrappedKey,
		connection.CredentialEnvVar, connection.Status, marshalTags(connection.Labels),
		connection.CreatedAt, connection.UpdatedAt)
	return err
}

func (s *SQLiteStore) UpdateCloudConnectionIfUnchanged(connection *CloudConnection, expected time.Time) (bool, error) {
	if connection == nil || expected.IsZero() {
		return false, errors.New("cloud connection and expected updated_at are required")
	}
	if err := connection.Validate(); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	locked, err := lockCloudConnectionTx(tx, connection.ID)
	if err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	var current time.Time
	var currentRaw, currentProvider, currentAzureCloud, currentAzureSubscriptionID, currentAzureResourceGroup string
	var currentAWSPartition, currentAWSAccountID, currentAWSRegion string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT), provider,
		COALESCE(azure_cloud, ''), COALESCE(azure_subscription_id, ''), COALESCE(azure_resource_group, ''),
		COALESCE(aws_partition, ''), COALESCE(aws_account_id, ''), COALESCE(aws_region, '')
		FROM cloud_connections WHERE id = ?`, connection.ID).Scan(&current, &currentRaw, &currentProvider,
		&currentAzureCloud, &currentAzureSubscriptionID, &currentAzureResourceGroup,
		&currentAWSPartition, &currentAWSAccountID, &currentAWSRegion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !current.Equal(expected) {
		return false, nil
	}
	providerChanged := currentProvider != connection.Provider
	scopeChanged := currentAzureCloud != connection.AzureCloud ||
		currentAzureSubscriptionID != connection.AzureSubscriptionID ||
		currentAzureResourceGroup != connection.AzureResourceGroup ||
		currentAWSPartition != connection.AWSPartition ||
		currentAWSAccountID != connection.AWSAccountID ||
		currentAWSRegion != connection.AWSRegion
	if providerChanged || scopeChanged {
		var references int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM execution_targets WHERE cloud_connection_id = ?`, connection.ID).Scan(&references); err != nil {
			return false, err
		}
		if references > 0 {
			if providerChanged {
				return false, ErrCloudConnectionProviderImmutable
			}
			return false, ErrCloudConnectionScopeImmutable
		}
	}
	connection.UpdatedAt = localVersionTimestamp()
	result, err := tx.Exec(`UPDATE cloud_connections SET name = ?, description = ?, provider = ?, auth_mode = ?,
		azure_cloud = ?, azure_tenant_id = ?, azure_subscription_id = ?, azure_resource_group = ?, azure_client_id = ?,
		aws_partition = ?, aws_account_id = ?, aws_region = ?, aws_access_key_id = ?, aws_role_arn = ?,
		client_secret_ciphertext = ?, client_secret_wrapped_key = ?, aws_secret_key_ciphertext = ?,
		aws_secret_key_wrapped_key = ?, aws_external_id_ciphertext = ?, aws_external_id_wrapped_key = ?,
		credential_env_var = ?, status = ?, labels_json = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`,
		connection.Name, connection.Description, connection.Provider, connection.AuthMode,
		connection.AzureCloud, connection.AzureTenantID, connection.AzureSubscriptionID,
		connection.AzureResourceGroup, connection.AzureClientID, connection.AWSPartition,
		connection.AWSAccountID, connection.AWSRegion, connection.AWSAccessKeyID, connection.AWSRoleARN,
		connection.ClientSecretCiphertext, connection.ClientSecretWrappedKey,
		connection.AWSSecretKeyCiphertext, connection.AWSSecretKeyWrappedKey,
		connection.AWSExternalIDCiphertext, connection.AWSExternalIDWrappedKey,
		connection.CredentialEnvVar, connection.Status, marshalTags(connection.Labels), connection.UpdatedAt,
		connection.ID, currentRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) UpdateCloudConnectionHealth(id, status, message string, checkedAt time.Time) error {
	result, err := s.db.Exec(`UPDATE cloud_connections SET last_checked_at = ?, last_check_status = ?, last_check_message = ? WHERE id = ?`,
		checkedAt, status, message, strings.TrimSpace(id))
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

func (s *SQLiteStore) GetCloudConnection(id string) (*CloudConnection, error) {
	return scanCloudConnection(s.db.QueryRow(cloudConnectionSelect+` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) ListCloudConnections() ([]*CloudConnection, error) {
	rows, err := s.db.Query(cloudConnectionSelect + ` ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := make([]*CloudConnection, 0)
	for rows.Next() {
		connection, err := scanCloudConnection(rows)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, rows.Err()
}

func (s *SQLiteStore) DeleteCloudConnectionIfUnreferenced(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	locked, err := lockCloudConnectionTx(tx, id)
	if err != nil {
		return false, err
	}
	if !locked {
		return false, sql.ErrNoRows
	}
	var references int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM execution_targets WHERE cloud_connection_id = ?`, strings.TrimSpace(id)).Scan(&references); err != nil {
		return false, err
	}
	if references > 0 {
		return false, nil
	}
	result, err := tx.Exec(`DELETE FROM cloud_connections WHERE id = ?`, strings.TrimSpace(id))
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
