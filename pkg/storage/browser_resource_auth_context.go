package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Context variants preserve the established authorizer's exact SQL predicates,
// decoders and parent-scope normalization. Resource reads use these methods so
// cancellation also interrupts acquisition of an occupied database connection.
// They do not introduce an alternative role or credential policy.
const maxBrowserResourceAuthBindings = 4096

func (s *SQLiteStore) GetUserBySubjectContext(ctx context.Context, subject string) (*User, error) {
	user, err := scanUser(s.db.QueryRowContext(ctx, userSelectColumns()+` FROM users WHERE subject = ?`, strings.TrimSpace(subject)))
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, user_id, scope_type, COALESCE(scope_id, ''), role, created_at, updated_at FROM user_role_bindings WHERE user_id = ? ORDER BY scope_type, scope_id, role`, user.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []*UserRoleBinding{}
	for rows.Next() {
		if len(bindings) >= maxBrowserResourceAuthBindings {
			return nil, errors.New("browser resource authorization binding limit exceeded")
		}
		var binding UserRoleBinding
		if err := rows.Scan(&binding.ID, &binding.UserID, &binding.ScopeType, &binding.ScopeID, &binding.Role, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		bindings = append(bindings, &binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	user.Bindings = bindings
	return user, nil
}

func (s *SQLiteStore) GetServiceAccountContext(ctx context.Context, id string) (*ServiceAccount, error) {
	return s.getServiceAccountByQueryContext(ctx, serviceAccountSelectColumns()+` FROM service_accounts WHERE id = ?`, id)
}

func (s *SQLiteStore) getServiceAccountByQueryContext(ctx context.Context, query string, args ...any) (*ServiceAccount, error) {
	var account ServiceAccount
	var active any
	var globalRolesJSON string
	var projectRolesJSON string
	var domainRolesJSON string
	var repositoryRolesJSON string
	var scopesJSON string
	var lastUsedAt sql.NullTime
	var expiresAt sql.NullTime
	var keyRotatedAt sql.NullTime
	var rotationIntervalDays sql.NullInt64
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&account.ID,
		&account.Name,
		&account.Description,
		&account.OwnerID,
		&account.ContactEmail,
		&account.KeyHash,
		&active,
		&globalRolesJSON,
		&projectRolesJSON,
		&domainRolesJSON,
		&repositoryRolesJSON,
		&scopesJSON,
		&account.CreatedAt,
		&account.UpdatedAt,
		&lastUsedAt,
		&expiresAt,
		&keyRotatedAt,
		&rotationIntervalDays,
	)
	if err != nil {
		return nil, err
	}
	account.Active = sqlBool(active)
	if lastUsedAt.Valid {
		account.LastUsedAt = &lastUsedAt.Time
	}
	if expiresAt.Valid {
		account.ExpiresAt = &expiresAt.Time
	}
	if keyRotatedAt.Valid {
		account.KeyRotatedAt = &keyRotatedAt.Time
	}
	if rotationIntervalDays.Valid {
		account.RotationIntervalDays = int(rotationIntervalDays.Int64)
	}
	if err := json.Unmarshal([]byte(globalRolesJSON), &account.GlobalRoles); err != nil && strings.TrimSpace(globalRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(projectRolesJSON), &account.ProjectRoles); err != nil && strings.TrimSpace(projectRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(domainRolesJSON), &account.DomainRoles); err != nil && strings.TrimSpace(domainRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(repositoryRolesJSON), &account.RepositoryRoles); err != nil && strings.TrimSpace(repositoryRolesJSON) != "" {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &account.Scopes); err != nil && strings.TrimSpace(scopesJSON) != "" {
		return nil, err
	}
	if account.ProjectRoles == nil {
		account.ProjectRoles = map[string][]string{}
	}
	if account.DomainRoles == nil {
		account.DomainRoles = map[string][]string{}
	}
	if account.RepositoryRoles == nil {
		account.RepositoryRoles = map[string][]string{}
	}
	populateServiceAccountDerivedFields(&account)
	return &account, nil
}

func (s *SQLiteStore) GetProjectContext(ctx context.Context, id string) (*Project, error) {
	var project Project
	var settingsJSON string
	err := s.db.QueryRowContext(ctx, `SELECT id, COALESCE(domain_id, ''), COALESCE(category_id, ''), name, COALESCE(settings_json, ''), created_at, updated_at FROM projects WHERE id = ?`, id).Scan(
		&project.ID, &project.DomainID, &project.CategoryID, &project.Name, &settingsJSON, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		return nil, err
	}
	project.DomainID = normalizeDomainID(project.DomainID)
	project.CategoryID = normalizeRepositoryCategoryID(project.CategoryID)
	if settingsJSON != "" && settingsJSON != "null" {
		if err := json.Unmarshal([]byte(settingsJSON), &project.Settings); err != nil {
			return nil, fmt.Errorf("unmarshal project settings: %w", err)
		}
	}
	return &project, nil
}

func (s *SQLiteStore) GetRepositoryCategoryContext(ctx context.Context, id string) (*RepositoryCategory, error) {
	var category RepositoryCategory
	err := s.db.QueryRowContext(ctx, `SELECT id, domain_id, repository_id, name, COALESCE(description, ''), COALESCE(sort_order, 0), created_at, updated_at FROM repository_categories WHERE id = ?`, normalizeRepositoryCategoryID(id)).Scan(
		&category.ID, &category.DomainID, &category.RepositoryID, &category.Name, &category.Description, &category.SortOrder, &category.CreatedAt, &category.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &category, nil
}

func (s *SQLiteStore) GetApplicationRepositoryContext(ctx context.Context, domainID string) (*ApplicationRepository, error) {
	var repository ApplicationRepository
	err := s.db.QueryRowContext(ctx, `SELECT id, domain_id, name, COALESCE(description, ''), created_at, updated_at FROM application_repositories WHERE domain_id = ?`, normalizeDomainID(domainID)).Scan(
		&repository.ID, &repository.DomainID, &repository.Name, &repository.Description, &repository.CreatedAt, &repository.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &repository, nil
}

func (s *SQLiteStore) GetLocalAuthSessionByTokenHashForGenerationContext(ctx context.Context, generation, tokenHash string) (*LocalAuthSession, error) {
	generation, err := canonicalLocalAuthGeneration(generation)
	if err != nil {
		return nil, err
	}
	tokenHash, err = canonicalLocalSessionTokenHash(tokenHash)
	if err != nil {
		return nil, err
	}
	session := &LocalAuthSession{}
	err = s.db.QueryRowContext(ctx, `SELECT sessions.token_hash, sessions.user_id, sessions.session_epoch, sessions.created_at, sessions.expires_at
		FROM local_auth_sessions AS sessions
		JOIN local_auth_runtime AS runtime ON runtime.singleton_key = ? AND runtime.generation = ?
		WHERE sessions.token_hash = ?`, "current", generation, tokenHash).Scan(
		&session.TokenHash, &session.UserID, &session.SessionEpoch, &session.CreatedAt, &session.ExpiresAt)
	return session, err
}

func (s *SQLiteStore) GetLocalAuthGenerationContext(ctx context.Context) (string, error) {
	var generation string
	if err := s.db.QueryRowContext(ctx, `SELECT generation FROM local_auth_runtime WHERE singleton_key = ?`, "current").Scan(&generation); err != nil {
		return "", err
	}
	return canonicalLocalAuthGeneration(generation)
}
