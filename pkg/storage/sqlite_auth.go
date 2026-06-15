package storage

// SQLite store — auth. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveServiceAccount(account *ServiceAccount) error {
	if account == nil {
		return fmt.Errorf("service account is required")
	}
	globalRolesJSON, err := json.Marshal(account.GlobalRoles)
	if err != nil {
		return err
	}
	projectRolesJSON, err := json.Marshal(account.ProjectRoles)
	if err != nil {
		return err
	}
	domainRolesJSON, err := json.Marshal(account.DomainRoles)
	if err != nil {
		return err
	}
	repositoryRolesJSON, err := json.Marshal(account.RepositoryRoles)
	if err != nil {
		return err
	}
	scopesJSON, err := json.Marshal(account.Scopes)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO service_accounts (id, name, description, key_hash, active, global_roles_json, project_roles_json, domain_roles_json, repository_roles_json, scopes_json, created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			key_hash=excluded.key_hash,
			active=excluded.active,
			global_roles_json=excluded.global_roles_json,
			project_roles_json=excluded.project_roles_json,
			domain_roles_json=excluded.domain_roles_json,
			repository_roles_json=excluded.repository_roles_json,
			scopes_json=excluded.scopes_json,
			updated_at=excluded.updated_at,
			last_used_at=excluded.last_used_at,
			expires_at=excluded.expires_at,
			key_rotated_at=excluded.key_rotated_at,
			rotation_interval_days=excluded.rotation_interval_days`,
		account.ID,
		account.Name,
		account.Description,
		account.KeyHash,
		s.boolValue(account.Active),
		string(globalRolesJSON),
		string(projectRolesJSON),
		string(domainRolesJSON),
		string(repositoryRolesJSON),
		string(scopesJSON),
		account.CreatedAt,
		account.UpdatedAt,
		account.LastUsedAt,
		account.ExpiresAt,
		account.KeyRotatedAt,
		account.RotationIntervalDays,
	)
	return err
}

func (s *SQLiteStore) GetServiceAccount(id string) (*ServiceAccount, error) {
	return s.getServiceAccountByQuery(serviceAccountSelectColumns()+` FROM service_accounts WHERE id = ?`, id)
}

func (s *SQLiteStore) GetServiceAccountByKeyHash(keyHash string) (*ServiceAccount, error) {
	return s.getServiceAccountByQuery(serviceAccountSelectColumns()+` FROM service_accounts WHERE key_hash = ? AND active = 1 AND (expires_at IS NULL OR expires_at > ?)`, keyHash, time.Now().UTC())
}

func serviceAccountSelectColumns() string {
	return `SELECT id, name, description, key_hash, active, global_roles_json, project_roles_json, COALESCE(domain_roles_json, ''), COALESCE(repository_roles_json, ''), COALESCE(scopes_json, ''), created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days`
}

func (s *SQLiteStore) getServiceAccountByQuery(query string, args ...any) (*ServiceAccount, error) {
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
	err := s.db.QueryRow(query, args...).Scan(
		&account.ID,
		&account.Name,
		&account.Description,
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

func (s *SQLiteStore) ListServiceAccounts() ([]*ServiceAccount, error) {
	rows, err := s.db.Query(serviceAccountSelectColumns() + ` FROM service_accounts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := []*ServiceAccount{}
	for rows.Next() {
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
		if err := rows.Scan(&account.ID, &account.Name, &account.Description, &account.KeyHash, &active, &globalRolesJSON, &projectRolesJSON, &domainRolesJSON, &repositoryRolesJSON, &scopesJSON, &account.CreatedAt, &account.UpdatedAt, &lastUsedAt, &expiresAt, &keyRotatedAt, &rotationIntervalDays); err != nil {
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
		accounts = append(accounts, &account)
	}
	return accounts, rows.Err()
}

func (s *SQLiteStore) RotateServiceAccountKey(id string, keyHash string, updatedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE service_accounts SET key_hash = ?, updated_at = ?, key_rotated_at = ? WHERE id = ?`, keyHash, updatedAt, updatedAt, id)
	return err
}

func populateServiceAccountDerivedFields(account *ServiceAccount) {
	if account == nil || account.RotationIntervalDays <= 0 {
		return
	}
	anchor := account.CreatedAt
	if account.KeyRotatedAt != nil {
		anchor = *account.KeyRotatedAt
	}
	due := anchor.AddDate(0, 0, account.RotationIntervalDays)
	account.RotationDueAt = &due
}

func (s *SQLiteStore) UpdateServiceAccountLastUsed(id string, lastUsedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE service_accounts SET last_used_at = ?, updated_at = ? WHERE id = ?`, lastUsedAt, lastUsedAt, id)
	return err
}

func (s *SQLiteStore) UpdateServiceAccountActive(id string, active bool, updatedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE service_accounts SET active = ?, updated_at = ? WHERE id = ?`, s.boolValue(active), updatedAt, id)
	return err
}

func (s *SQLiteStore) DeleteServiceAccount(id string) error {
	_, err := s.db.Exec(`DELETE FROM service_accounts WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) SaveUser(user *User) error {
	if user == nil {
		return fmt.Errorf("user is required")
	}
	user.Subject = strings.TrimSpace(user.Subject)
	if user.Subject == "" {
		return fmt.Errorf("user subject is required")
	}
	_, err := s.db.Exec(`INSERT INTO users (id, subject, email, display_name, active, created_at, updated_at, last_seen_at, disabled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			subject=excluded.subject,
			email=excluded.email,
			display_name=excluded.display_name,
			active=excluded.active,
			updated_at=excluded.updated_at,
			last_seen_at=excluded.last_seen_at,
			disabled_at=excluded.disabled_at`,
		user.ID,
		user.Subject,
		user.Email,
		user.DisplayName,
		s.boolValue(user.Active),
		user.CreatedAt,
		user.UpdatedAt,
		user.LastSeenAt,
		user.DisabledAt,
	)
	return err
}

func (s *SQLiteStore) GetUser(id string) (*User, error) {
	return s.getUserByQuery(userSelectColumns()+` FROM users WHERE id = ?`, id)
}

func (s *SQLiteStore) GetUserBySubject(subject string) (*User, error) {
	return s.getUserByQuery(userSelectColumns()+` FROM users WHERE subject = ?`, strings.TrimSpace(subject))
}

func (s *SQLiteStore) ListUsers(filter UserFilter) ([]*User, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := strings.Builder{}
	query.WriteString(userSelectColumns() + ` FROM users WHERE 1 = 1`)
	args := []any{}
	if q := strings.TrimSpace(filter.Query); q != "" {
		query.WriteString(` AND (subject LIKE ? OR email LIKE ? OR display_name LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	if filter.Active != nil {
		query.WriteString(` AND active = ?`)
		args = append(args, s.boolValue(*filter.Active))
	}
	query.WriteString(` ORDER BY updated_at DESC LIMIT ? OFFSET ?`)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []*User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		bindings, err := s.ListUserRoleBindings(user.ID)
		if err != nil {
			return nil, err
		}
		user.Bindings = bindings
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *SQLiteStore) UpdateUserActive(id string, active bool, updatedAt time.Time) error {
	var disabledAt any
	if !active {
		disabledAt = updatedAt
	}
	_, err := s.db.Exec(`UPDATE users SET active = ?, updated_at = ?, disabled_at = ? WHERE id = ?`, s.boolValue(active), updatedAt, disabledAt, id)
	return err
}

func (s *SQLiteStore) DeleteUser(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The declared FK ON DELETE CASCADE is non-functional on both backends (SQLite
	// FK enforcement is off; the Postgres schema rewriter strips FOREIGN KEY lines),
	// so delete role bindings explicitly. Otherwise a deleted user leaves orphaned
	// RBAC grants that a reused user id would silently inherit.
	if _, err := tx.Exec(`DELETE FROM user_role_bindings WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveUserRoleBinding(binding *UserRoleBinding) error {
	if binding == nil {
		return fmt.Errorf("user role binding is required")
	}
	_, err := s.db.Exec(`INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, scope_type, scope_id, role) DO UPDATE SET
			updated_at=excluded.updated_at`,
		binding.ID,
		binding.UserID,
		binding.ScopeType,
		binding.ScopeID,
		binding.Role,
		binding.CreatedAt,
		binding.UpdatedAt,
	)
	return err
}

func (s *SQLiteStore) DeleteUserRoleBinding(id string) error {
	_, err := s.db.Exec(`DELETE FROM user_role_bindings WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) ListUserRoleBindings(userID string) ([]*UserRoleBinding, error) {
	rows, err := s.db.Query(`SELECT id, user_id, scope_type, COALESCE(scope_id, ''), role, created_at, updated_at FROM user_role_bindings WHERE user_id = ? ORDER BY scope_type, scope_id, role`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := []*UserRoleBinding{}
	for rows.Next() {
		var binding UserRoleBinding
		if err := rows.Scan(&binding.ID, &binding.UserID, &binding.ScopeType, &binding.ScopeID, &binding.Role, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		bindings = append(bindings, &binding)
	}
	return bindings, rows.Err()
}

func userSelectColumns() string {
	return `SELECT id, subject, COALESCE(email, ''), COALESCE(display_name, ''), active, created_at, updated_at, last_seen_at, disabled_at`
}

func (s *SQLiteStore) getUserByQuery(query string, args ...any) (*User, error) {
	user, err := scanUser(s.db.QueryRow(query, args...))
	if err != nil {
		return nil, err
	}
	bindings, err := s.ListUserRoleBindings(user.ID)
	if err != nil {
		return nil, err
	}
	user.Bindings = bindings
	return user, nil
}

func scanUser(scanner userScanner) (*User, error) {
	var user User
	var active any
	var lastSeenAt sql.NullTime
	var disabledAt sql.NullTime
	if err := scanner.Scan(&user.ID, &user.Subject, &user.Email, &user.DisplayName, &active, &user.CreatedAt, &user.UpdatedAt, &lastSeenAt, &disabledAt); err != nil {
		return nil, err
	}
	user.Active = sqlBool(active)
	if lastSeenAt.Valid {
		user.LastSeenAt = &lastSeenAt.Time
	}
	if disabledAt.Valid {
		user.DisabledAt = &disabledAt.Time
	}
	return &user, nil
}

func (s *SQLiteStore) SaveAuditEvent(event *AuditEvent) error {
	if event == nil {
		return fmt.Errorf("audit event is required")
	}
	detailsJSON, err := json.Marshal(event.Details)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO audit_events (id, action, actor_type, actor_subject, actor_display_name, project_id, domain_id, resource_type, resource_id, status, details_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.Action,
		event.ActorType,
		event.ActorSubject,
		event.ActorDisplayName,
		event.ProjectID,
		event.DomainID,
		event.ResourceType,
		event.ResourceID,
		event.Status,
		string(detailsJSON),
		event.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) ListAuditEvents(filter AuditEventFilter) ([]*AuditEvent, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := strings.Builder{}
	query.WriteString(`SELECT id, action, actor_type, actor_subject, actor_display_name, COALESCE(project_id, ''), COALESCE(domain_id, ''), resource_type, resource_id, status, details_json, created_at FROM audit_events WHERE 1 = 1`)
	args := make([]any, 0, 8)
	appendFilter := func(column string, value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		query.WriteString(` AND ` + column + ` = ?`)
		args = append(args, trimmed)
	}
	appendFilter("action", filter.Action)
	appendFilter("actor_type", filter.ActorType)
	appendFilter("actor_subject", filter.ActorSubject)
	appendFilter("project_id", filter.ProjectID)
	appendFilter("domain_id", filter.DomainID)
	appendFilter("resource_type", filter.ResourceType)
	appendFilter("resource_id", filter.ResourceID)
	appendFilter("status", filter.Status)
	if filter.CreatedAfter != nil {
		query.WriteString(` AND created_at >= ?`)
		args = append(args, *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query.WriteString(` AND created_at <= ?`)
		args = append(args, *filter.CreatedBefore)
	}
	query.WriteString(` ORDER BY created_at DESC LIMIT ? OFFSET ?`)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []*AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		var detailsJSON string
		if err := rows.Scan(&event.ID, &event.Action, &event.ActorType, &event.ActorSubject, &event.ActorDisplayName, &event.ProjectID, &event.DomainID, &event.ResourceType, &event.ResourceID, &event.Status, &detailsJSON, &event.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(detailsJSON), &event.Details); err != nil && strings.TrimSpace(detailsJSON) != "" {
			return nil, err
		}
		events = append(events, &event)
	}
	return events, rows.Err()
}
