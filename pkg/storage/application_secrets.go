package storage

import (
	"errors"
	"strings"
)

// ApplicationSecretStorage adapts the existing encrypted and external-provider
// stores to application-owned records. The virtual namespace is deliberately
// distinct from every project ID at the provider and encryption boundaries.
type ApplicationSecretStorage struct{ *SQLiteStore }

// A NUL-prefixed virtual ID keeps the provider's hashed physical namespace
// and the encryption AAD disjoint from ordinary project identifiers.
const applicationSecretPrefix = "\x00application:"

func ApplicationSecretScopeID(applicationID string) string {
	return applicationSecretPrefix + strings.TrimSpace(applicationID)
}

func applicationIDFromSecretScope(scopeID string) (string, error) {
	if !strings.HasPrefix(scopeID, applicationSecretPrefix) {
		return "", errors.New("application secret scope is required")
	}
	id := strings.TrimSpace(strings.TrimPrefix(scopeID, applicationSecretPrefix))
	if id == "" {
		return "", errors.New("application id is required")
	}
	return id, nil
}

func (s ApplicationSecretStorage) CreateProjectSecretCipher(secret *ProjectSecretCipher) (bool, error) {
	if _, err := s.validateProjectSecretCipher(secret); err != nil {
		return false, err
	}
	id, err := applicationIDFromSecretScope(secret.ProjectID)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`INSERT INTO application_secrets
		(application_id, secret_name, provider, ciphertext, wrapped_key_json, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(application_id, secret_name) DO NOTHING`,
		id, secret.SecretName, secret.Provider, secret.Ciphertext, secret.WrappedKeyJSON,
		secret.CreatedAt, secret.UpdatedAt, strings.TrimSpace(secret.CreatedBy))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s ApplicationSecretStorage) ReplaceProjectSecretCipher(secret *ProjectSecretCipher) (bool, error) {
	if _, err := s.validateProjectSecretCipher(secret); err != nil {
		return false, err
	}
	id, err := applicationIDFromSecretScope(secret.ProjectID)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`UPDATE application_secrets
		SET provider = ?, ciphertext = ?, wrapped_key_json = ?, updated_at = ?, created_by = ?
		WHERE application_id = ? AND secret_name = ?`,
		secret.Provider, secret.Ciphertext, secret.WrappedKeyJSON, secret.UpdatedAt,
		strings.TrimSpace(secret.CreatedBy), id, secret.SecretName)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s ApplicationSecretStorage) DeleteProjectSecretCipherIfCreatedBy(scopeID, name, actor string) (bool, error) {
	id, err := applicationIDFromSecretScope(scopeID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(actor) == "" {
		return false, errors.New("secret name and creator are required")
	}
	result, err := s.db.Exec(`DELETE FROM application_secrets WHERE application_id = ? AND secret_name = ? AND created_by = ?`, id, name, actor)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s ApplicationSecretStorage) GetProjectSecretCipher(scopeID, name string) (*ProjectSecretCipher, error) {
	id, err := applicationIDFromSecretScope(scopeID)
	if err != nil {
		return nil, err
	}
	var record ProjectSecretCipher
	err = s.db.QueryRow(`SELECT secret_name, provider, ciphertext, wrapped_key_json,
		created_at, updated_at, COALESCE(created_by, '') FROM application_secrets
		WHERE application_id = ? AND secret_name = ?`, id, name).Scan(
		&record.SecretName, &record.Provider, &record.Ciphertext, &record.WrappedKeyJSON,
		&record.CreatedAt, &record.UpdatedAt, &record.CreatedBy)
	if err != nil {
		return nil, err
	}
	record.ProjectID = scopeID
	return &record, nil
}

func (s ApplicationSecretStorage) ListProjectSecretMetadata(scopeID string) ([]ProjectSecretMetadata, error) {
	id, err := applicationIDFromSecretScope(scopeID)
	if err != nil {
		return nil, err
	}
	return s.listApplicationSecretMetadata("application_secrets", id, scopeID)
}

func (s ApplicationSecretStorage) CreateProjectSecretMetadata(metadata *ProjectSecretMetadata) (bool, error) {
	if err := validateExternalProjectSecretMetadata(s.SQLiteStore, metadata); err != nil {
		return false, err
	}
	id, err := applicationIDFromSecretScope(metadata.ProjectID)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`INSERT INTO application_secret_provider_metadata
		(application_id, secret_name, provider, created_at, updated_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(application_id, secret_name) DO NOTHING`,
		id, metadata.SecretName, metadata.Provider, metadata.CreatedAt, metadata.UpdatedAt,
		strings.TrimSpace(metadata.CreatedBy))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s ApplicationSecretStorage) ReplaceProjectSecretMetadata(metadata *ProjectSecretMetadata) (bool, error) {
	if err := validateExternalProjectSecretMetadata(s.SQLiteStore, metadata); err != nil {
		return false, err
	}
	id, err := applicationIDFromSecretScope(metadata.ProjectID)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`UPDATE application_secret_provider_metadata SET provider = ?, updated_at = ?, created_by = ?
		WHERE application_id = ? AND secret_name = ?`, metadata.Provider, metadata.UpdatedAt,
		strings.TrimSpace(metadata.CreatedBy), id, metadata.SecretName)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (s ApplicationSecretStorage) GetProjectSecretMetadata(scopeID, name string) (*ProjectSecretMetadata, error) {
	id, err := applicationIDFromSecretScope(scopeID)
	if err != nil {
		return nil, err
	}
	var metadata ProjectSecretMetadata
	err = s.db.QueryRow(`SELECT secret_name, provider, created_at, updated_at, COALESCE(created_by, '')
		FROM application_secret_provider_metadata WHERE application_id = ? AND secret_name = ?`,
		id, name).Scan(&metadata.SecretName, &metadata.Provider, &metadata.CreatedAt,
		&metadata.UpdatedAt, &metadata.CreatedBy)
	if err != nil {
		return nil, err
	}
	metadata.ProjectID = scopeID
	return &metadata, nil
}

func (s ApplicationSecretStorage) ListExternalProjectSecretMetadata(scopeID string) ([]ProjectSecretMetadata, error) {
	id, err := applicationIDFromSecretScope(scopeID)
	if err != nil {
		return nil, err
	}
	return s.listApplicationSecretMetadata("application_secret_provider_metadata", id, scopeID)
}

func (s ApplicationSecretStorage) listApplicationSecretMetadata(table, id, scopeID string) ([]ProjectSecretMetadata, error) {
	// table is selected only by internal callers above.
	rows, err := s.db.Query(`SELECT secret_name, provider, created_at, updated_at, COALESCE(created_by, '')
		FROM `+table+` WHERE application_id = ? ORDER BY secret_name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProjectSecretMetadata, 0)
	for rows.Next() {
		var metadata ProjectSecretMetadata
		if err := rows.Scan(&metadata.SecretName, &metadata.Provider, &metadata.CreatedAt,
			&metadata.UpdatedAt, &metadata.CreatedBy); err != nil {
			return nil, err
		}
		metadata.ProjectID = scopeID
		result = append(result, metadata)
	}
	return result, rows.Err()
}

func (s ApplicationSecretStorage) DeleteProjectSecretMetadata(scopeID, name string) error {
	id, err := applicationIDFromSecretScope(scopeID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM application_secret_provider_metadata WHERE application_id = ? AND secret_name = ?`, id, name)
	return err
}

var _ ProjectSecretCipherStore = ApplicationSecretStorage{}
var _ ProjectSecretMetadataStore = ApplicationSecretStorage{}
