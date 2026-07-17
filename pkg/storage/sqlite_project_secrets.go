package storage

import (
	"errors"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveProjectSecretCipher(secret *ProjectSecretCipher) error {
	if _, err := s.validateProjectSecretCipher(secret); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO project_secrets (
			project_id, secret_name, provider, ciphertext, wrapped_key_json,
			created_at, updated_at, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, secret_name) DO UPDATE SET
			provider = excluded.provider,
			ciphertext = excluded.ciphertext,
			wrapped_key_json = excluded.wrapped_key_json,
			updated_at = excluded.updated_at,
			created_by = excluded.created_by
	`, secret.ProjectID, secret.SecretName, secret.Provider, secret.Ciphertext,
		secret.WrappedKeyJSON, secret.CreatedAt, secret.UpdatedAt, strings.TrimSpace(secret.CreatedBy))
	return err
}

func (s *SQLiteStore) CreateProjectSecretCipher(secret *ProjectSecretCipher) (bool, error) {
	if _, err := s.validateProjectSecretCipher(secret); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`
		INSERT INTO project_secrets (
			project_id, secret_name, provider, ciphertext, wrapped_key_json,
			created_at, updated_at, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, secret_name) DO NOTHING
	`, secret.ProjectID, secret.SecretName, secret.Provider, secret.Ciphertext,
		secret.WrappedKeyJSON, secret.CreatedAt, secret.UpdatedAt, strings.TrimSpace(secret.CreatedBy))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) ReplaceProjectSecretCipher(secret *ProjectSecretCipher) (bool, error) {
	if _, err := s.validateProjectSecretCipher(secret); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`
		UPDATE project_secrets
		SET provider = ?, ciphertext = ?, wrapped_key_json = ?,
		    updated_at = ?, created_by = ?
		WHERE project_id = ? AND secret_name = ?
	`, secret.Provider, secret.Ciphertext, secret.WrappedKeyJSON, secret.UpdatedAt,
		strings.TrimSpace(secret.CreatedBy), secret.ProjectID, secret.SecretName)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) DeleteProjectSecretCipherIfCreatedBy(projectID, secretName, createdBy string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errors.New("project secret storage is not configured")
	}
	projectID = strings.TrimSpace(projectID)
	secretName = strings.TrimSpace(secretName)
	createdBy = strings.TrimSpace(createdBy)
	if projectID == "" || secretName == "" || createdBy == "" {
		return false, errors.New("project, secret name, and creator are required")
	}
	result, err := s.db.Exec(`
		DELETE FROM project_secrets
		WHERE project_id = ? AND secret_name = ? AND created_by = ?
	`, projectID, secretName, createdBy)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) validateProjectSecretCipher(secret *ProjectSecretCipher) (*ProjectSecretCipher, error) {
	if s == nil || s.db == nil || secret == nil {
		return nil, errors.New("project secret storage is not configured")
	}
	secret.ProjectID = strings.TrimSpace(secret.ProjectID)
	secret.SecretName = strings.TrimSpace(secret.SecretName)
	secret.Provider = strings.TrimSpace(secret.Provider)
	secret.Ciphertext = strings.TrimSpace(secret.Ciphertext)
	secret.WrappedKeyJSON = strings.TrimSpace(secret.WrappedKeyJSON)
	if secret.ProjectID == "" || secret.SecretName == "" || secret.Provider == "" ||
		secret.Ciphertext == "" || secret.WrappedKeyJSON == "" {
		return nil, errors.New("project secret encrypted fields are required")
	}
	now := time.Now().UTC()
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = now
	}
	if secret.UpdatedAt.IsZero() {
		secret.UpdatedAt = now
	}
	return secret, nil
}

func (s *SQLiteStore) GetProjectSecretCipher(projectID, secretName string) (*ProjectSecretCipher, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("project secret storage is not configured")
	}
	var secret ProjectSecretCipher
	err := s.db.QueryRow(`
		SELECT project_id, secret_name, provider, ciphertext, wrapped_key_json,
		       created_at, updated_at, COALESCE(created_by, '')
		FROM project_secrets
		WHERE project_id = ? AND secret_name = ?
	`, strings.TrimSpace(projectID), strings.TrimSpace(secretName)).Scan(
		&secret.ProjectID, &secret.SecretName, &secret.Provider, &secret.Ciphertext,
		&secret.WrappedKeyJSON, &secret.CreatedAt, &secret.UpdatedAt, &secret.CreatedBy,
	)
	if err != nil {
		return nil, err
	}
	return &secret, nil
}

func (s *SQLiteStore) ListProjectSecretMetadata(projectID string) ([]ProjectSecretMetadata, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("project secret storage is not configured")
	}
	rows, err := s.db.Query(`
		SELECT project_id, secret_name, provider, created_at, updated_at, COALESCE(created_by, '')
		FROM project_secrets
		WHERE project_id = ?
		ORDER BY secret_name
	`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProjectSecretMetadata, 0)
	for rows.Next() {
		var metadata ProjectSecretMetadata
		if err := rows.Scan(&metadata.ProjectID, &metadata.SecretName, &metadata.Provider,
			&metadata.CreatedAt, &metadata.UpdatedAt, &metadata.CreatedBy); err != nil {
			return nil, err
		}
		result = append(result, metadata)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []ProjectSecretMetadata{}
	}
	return result, nil
}

func (s *SQLiteStore) CreateProjectSecretMetadata(metadata *ProjectSecretMetadata) (bool, error) {
	if err := validateExternalProjectSecretMetadata(s, metadata); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`
		INSERT INTO project_secret_provider_metadata (
			project_id, secret_name, provider, created_at, updated_at, created_by
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, secret_name) DO NOTHING
	`, metadata.ProjectID, metadata.SecretName, metadata.Provider, metadata.CreatedAt,
		metadata.UpdatedAt, strings.TrimSpace(metadata.CreatedBy))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) ReplaceProjectSecretMetadata(metadata *ProjectSecretMetadata) (bool, error) {
	if err := validateExternalProjectSecretMetadata(s, metadata); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`
		UPDATE project_secret_provider_metadata
		SET provider = ?, updated_at = ?, created_by = ?
		WHERE project_id = ? AND secret_name = ?
	`, metadata.Provider, metadata.UpdatedAt, strings.TrimSpace(metadata.CreatedBy),
		metadata.ProjectID, metadata.SecretName)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) GetProjectSecretMetadata(projectID, secretName string) (*ProjectSecretMetadata, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("project secret metadata storage is not configured")
	}
	var metadata ProjectSecretMetadata
	err := s.db.QueryRow(`
		SELECT project_id, secret_name, provider, created_at, updated_at, COALESCE(created_by, '')
		FROM project_secret_provider_metadata
		WHERE project_id = ? AND secret_name = ?
	`, strings.TrimSpace(projectID), strings.TrimSpace(secretName)).Scan(
		&metadata.ProjectID, &metadata.SecretName, &metadata.Provider,
		&metadata.CreatedAt, &metadata.UpdatedAt, &metadata.CreatedBy,
	)
	if err != nil {
		return nil, err
	}
	return &metadata, nil
}

func (s *SQLiteStore) ListExternalProjectSecretMetadata(projectID string) ([]ProjectSecretMetadata, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("project secret metadata storage is not configured")
	}
	rows, err := s.db.Query(`
		SELECT project_id, secret_name, provider, created_at, updated_at, COALESCE(created_by, '')
		FROM project_secret_provider_metadata
		WHERE project_id = ?
		ORDER BY secret_name
	`, strings.TrimSpace(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProjectSecretMetadata, 0)
	for rows.Next() {
		var metadata ProjectSecretMetadata
		if err := rows.Scan(&metadata.ProjectID, &metadata.SecretName, &metadata.Provider,
			&metadata.CreatedAt, &metadata.UpdatedAt, &metadata.CreatedBy); err != nil {
			return nil, err
		}
		result = append(result, metadata)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DeleteProjectSecretMetadata(projectID, secretName string) error {
	if s == nil || s.db == nil {
		return errors.New("project secret metadata storage is not configured")
	}
	_, err := s.db.Exec(`
		DELETE FROM project_secret_provider_metadata
		WHERE project_id = ? AND secret_name = ?
	`, strings.TrimSpace(projectID), strings.TrimSpace(secretName))
	return err
}

func validateExternalProjectSecretMetadata(s *SQLiteStore, metadata *ProjectSecretMetadata) error {
	if s == nil || s.db == nil || metadata == nil {
		return errors.New("project secret metadata storage is not configured")
	}
	metadata.ProjectID = strings.TrimSpace(metadata.ProjectID)
	metadata.SecretName = strings.TrimSpace(metadata.SecretName)
	metadata.Provider = strings.TrimSpace(metadata.Provider)
	if metadata.ProjectID == "" || metadata.SecretName == "" || metadata.Provider == "" {
		return errors.New("project secret metadata fields are required")
	}
	now := time.Now().UTC()
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = now
	}
	if metadata.UpdatedAt.IsZero() {
		metadata.UpdatedAt = now
	}
	return nil
}

var _ ProjectSecretCipherStore = (*SQLiteStore)(nil)
var _ ProjectSecretCipherStore = (*PostgresStore)(nil)
var _ ProjectSecretMetadataStore = (*SQLiteStore)(nil)
var _ ProjectSecretMetadataStore = (*PostgresStore)(nil)
