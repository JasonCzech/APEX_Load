package storage

// SQLite store — auth. Split from sqlite.go (same package, behavior-preserving).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	storageViewerRole       = "viewer"
	storageEditorRole       = "editor"
	storageProjectAdminRole = "project_admin"
	storageCIRunnerRole     = "ci_runner"
	storageSystemAdminRole  = "system_admin"
)

// ErrRoleScopeNotFound means a durable scoped grant references an ownership
// anchor that does not exist (or was deleted while the mutation was in flight).
// Callers may safely surface this as a validation/conflict error rather than an
// opaque storage failure.
var ErrRoleScopeNotFound = errors.New("role scope not found")

// ErrUserNotFound means a role-binding write lost its owning user (or was
// given an owner that never existed). PostgreSQL compatibility migrations do
// not retain schema-level foreign keys, so this invariant is enforced under an
// explicit row lock in the write transaction.
var ErrUserNotFound = errors.New("user not found")

func normalizeAuthorizationRole(role string) (string, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case storageViewerRole, storageEditorRole, storageProjectAdminRole, storageCIRunnerRole, storageSystemAdminRole:
		return role, nil
	case "":
		return "", errors.New("authorization role is required")
	default:
		return "", fmt.Errorf("unsupported authorization role %q", role)
	}
}

func normalizeAuthorizationRoles(roles []string) ([]string, error) {
	if roles == nil {
		return nil, nil
	}
	set := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		normalized, err := normalizeAuthorizationRole(role)
		if err != nil {
			return nil, err
		}
		set[normalized] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for role := range set {
		out = append(out, role)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeUserRoleBindingValues(binding *UserRoleBinding) error {
	if binding == nil {
		return errors.New("user role binding is required")
	}
	binding.ID = strings.TrimSpace(binding.ID)
	if binding.ID == "" {
		return errors.New("user role binding id is required")
	}
	binding.UserID = strings.TrimSpace(binding.UserID)
	if binding.UserID == "" {
		return errors.New("user role binding user id is required")
	}
	role, err := normalizeAuthorizationRole(binding.Role)
	if err != nil {
		return err
	}
	binding.Role = role
	binding.ScopeType = strings.ToLower(strings.TrimSpace(binding.ScopeType))
	binding.ScopeID = strings.TrimSpace(binding.ScopeID)
	switch binding.ScopeType {
	case "global":
		binding.ScopeID = ""
	case "project":
		if binding.ScopeID == "" {
			return errors.New("project role scope id is required")
		}
		binding.ScopeID = normalizeProjectID(binding.ScopeID)
	case "domain":
		if binding.ScopeID == "" {
			return errors.New("domain role scope id is required")
		}
		binding.ScopeID = normalizeDomainID(binding.ScopeID)
	case "application_repository":
		if binding.ScopeID == "" {
			return errors.New("application repository role scope id is required")
		}
	default:
		return fmt.Errorf("unsupported role scope type %q", binding.ScopeType)
	}
	return nil
}

func normalizeServiceAccountAuthorization(account *ServiceAccount) error {
	if account == nil {
		return errors.New("service account is required")
	}
	var err error
	account.GlobalRoles, err = normalizeAuthorizationRoles(account.GlobalRoles)
	if err != nil {
		return err
	}
	for label, scopedRoles := range map[string]map[string][]string{
		"project":                account.ProjectRoles,
		"domain":                 account.DomainRoles,
		"application repository": account.RepositoryRoles,
	} {
		for scopeID, roles := range scopedRoles {
			normalized, err := normalizeAuthorizationRoles(roles)
			if err != nil {
				return fmt.Errorf("%s %q roles: %w", label, strings.TrimSpace(scopeID), err)
			}
			scopedRoles[scopeID] = normalized
		}
	}
	return nil
}

func lockRoleScope(tx *storeTx, scopeType, scopeID string) error {
	var table string
	switch scopeType {
	case "project":
		table = "projects"
	case "domain":
		table = "domains"
	case "application_repository":
		table = "application_repositories"
	default:
		return fmt.Errorf("unsupported role scope type %q", scopeType)
	}
	result, err := tx.Exec(`UPDATE `+table+` SET updated_at = updated_at WHERE id = ?`, scopeID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%w: %s %q", ErrRoleScopeNotFound, scopeType, scopeID)
	}
	return nil
}

func normalizeAndLockUserRoleBindingScope(tx *storeTx, binding *UserRoleBinding) error {
	if err := normalizeUserRoleBindingValues(binding); err != nil {
		return err
	}
	switch binding.ScopeType {
	case "global":
		return nil
	case "project":
	case "domain":
	case "application_repository":
	}
	return lockRoleScope(tx, binding.ScopeType, binding.ScopeID)
}

func lockUserRoleBindingOwner(tx *storeTx, binding *UserRoleBinding) error {
	if binding == nil {
		return fmt.Errorf("user role binding is required")
	}
	binding.UserID = strings.TrimSpace(binding.UserID)
	if binding.UserID == "" {
		return fmt.Errorf("user role binding user id is required")
	}
	result, err := tx.Exec(`UPDATE users SET updated_at = updated_at WHERE id = ?`, binding.UserID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%w: %q", ErrUserNotFound, binding.UserID)
	}
	return nil
}

func sortedGrantedScopeIDs(roles map[string][]string, normalize func(string) string) []string {
	ids := make([]string, 0, len(roles))
	for id, grantedRoles := range roles {
		if len(grantedRoles) == 0 {
			continue
		}
		id = normalize(id)
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// lockServiceAccountRoleScopes validates every embedded scoped grant and pairs
// the write with project/domain deletion. Lock order is deterministic across
// controllers so overlapping account edits cannot deadlock each other merely
// because JSON map iteration order differs.
func lockServiceAccountRoleScopes(tx *storeTx, accounts ...*ServiceAccount) error {
	domainRoles := map[string][]string{}
	repositoryRoles := map[string][]string{}
	projectRoles := map[string][]string{}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		for id, roles := range account.DomainRoles {
			if len(roles) > 0 {
				domainRoles[id] = roles
			}
		}
		for id, roles := range account.RepositoryRoles {
			if len(roles) > 0 {
				repositoryRoles[id] = roles
			}
		}
		for id, roles := range account.ProjectRoles {
			if len(roles) > 0 {
				projectRoles[id] = roles
			}
		}
	}
	for _, id := range sortedGrantedScopeIDs(domainRoles, normalizeDomainID) {
		if err := lockRoleScope(tx, "domain", id); err != nil {
			return err
		}
	}
	for _, id := range sortedGrantedScopeIDs(repositoryRoles, strings.TrimSpace) {
		if err := lockRoleScope(tx, "application_repository", id); err != nil {
			return err
		}
	}
	for _, id := range sortedGrantedScopeIDs(projectRoles, normalizeProjectID) {
		if err := lockRoleScope(tx, "project", id); err != nil {
			return err
		}
	}
	return nil
}

func grantedScopeIDSet(roles map[string][]string, normalize func(string) string) map[string]struct{} {
	set := make(map[string]struct{}, len(roles))
	for id, grantedRoles := range roles {
		if len(grantedRoles) == 0 {
			continue
		}
		if id = normalize(id); id != "" {
			set[id] = struct{}{}
		}
	}
	return set
}

// lockServiceAccountRoleScopeTransition strictly validates every requested
// scope while best-effort locking scopes that are only being removed. The
// latter may already be absent in a legacy database; administrators must still
// be able to clean a dormant dangling grant instead of being permanently
// blocked by the invariant that the cleanup is trying to restore.
func lockServiceAccountRoleScopeTransition(tx *storeTx, current, next *ServiceAccount) error {
	type scopeTransition struct {
		scopeType string
		current   map[string]struct{}
		next      map[string]struct{}
	}
	transitions := []scopeTransition{
		{scopeType: "domain", current: grantedScopeIDSet(current.DomainRoles, normalizeDomainID), next: grantedScopeIDSet(next.DomainRoles, normalizeDomainID)},
		{scopeType: "application_repository", current: grantedScopeIDSet(current.RepositoryRoles, strings.TrimSpace), next: grantedScopeIDSet(next.RepositoryRoles, strings.TrimSpace)},
		{scopeType: "project", current: grantedScopeIDSet(current.ProjectRoles, normalizeProjectID), next: grantedScopeIDSet(next.ProjectRoles, normalizeProjectID)},
	}
	for _, transition := range transitions {
		union := make(map[string]struct{}, len(transition.current)+len(transition.next))
		for id := range transition.current {
			union[id] = struct{}{}
		}
		for id := range transition.next {
			union[id] = struct{}{}
		}
		ids := make([]string, 0, len(union))
		for id := range union {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			err := lockRoleScope(tx, transition.scopeType, id)
			_, required := transition.next[id]
			if err != nil && !(errors.Is(err, ErrRoleScopeNotFound) && !required) {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) SaveServiceAccount(account *ServiceAccount) error {
	if account == nil {
		return fmt.Errorf("service account is required")
	}
	if err := normalizeServiceAccountAuthorization(account); err != nil {
		return err
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockServiceAccountRoleScopes(tx, account); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO service_accounts (id, name, description, owner_id, contact_email, key_hash, active, global_roles_json, project_roles_json, domain_roles_json, repository_roles_json, scopes_json, created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		account.ID,
		account.Name,
		account.Description,
		account.OwnerID,
		account.ContactEmail,
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
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateServiceAccountMetadata performs an optimistic metadata-only update.
// Keeping key_hash, key_rotated_at, and last_used_at out of this statement is
// intentional: a stale PATCH must never undo a concurrent rotation, restore a
// deleted credential, or roll back authentication telemetry.
func (s *SQLiteStore) UpdateServiceAccountMetadata(account *ServiceAccount, expectedUpdatedAt time.Time) (bool, error) {
	if account == nil {
		return false, fmt.Errorf("service account is required")
	}
	if err := normalizeServiceAccountAuthorization(account); err != nil {
		return false, err
	}
	globalRolesJSON, err := json.Marshal(account.GlobalRoles)
	if err != nil {
		return false, err
	}
	projectRolesJSON, err := json.Marshal(account.ProjectRoles)
	if err != nil {
		return false, err
	}
	domainRolesJSON, err := json.Marshal(account.DomainRoles)
	if err != nil {
		return false, err
	}
	repositoryRolesJSON, err := json.Marshal(account.RepositoryRoles)
	if err != nil {
		return false, err
	}
	scopesJSON, err := json.Marshal(account.Scopes)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentProjectRolesJSON, currentDomainRolesJSON, currentRepositoryRolesJSON string
	if err := tx.QueryRow(`SELECT COALESCE(project_roles_json, ''), COALESCE(domain_roles_json, ''), COALESCE(repository_roles_json, '')
		FROM service_accounts WHERE id = ?`, account.ID).Scan(&currentProjectRolesJSON, &currentDomainRolesJSON, &currentRepositoryRolesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	currentScopes := &ServiceAccount{}
	for _, encoded := range []struct {
		label string
		raw   string
		into  *map[string][]string
	}{
		{label: "project", raw: currentProjectRolesJSON, into: &currentScopes.ProjectRoles},
		{label: "domain", raw: currentDomainRolesJSON, into: &currentScopes.DomainRoles},
		{label: "application repository", raw: currentRepositoryRolesJSON, into: &currentScopes.RepositoryRoles},
	} {
		if strings.TrimSpace(encoded.raw) == "" || strings.TrimSpace(encoded.raw) == "null" {
			continue
		}
		if err := json.Unmarshal([]byte(encoded.raw), encoded.into); err != nil {
			return false, fmt.Errorf("decode current service account %s roles: %w", encoded.label, err)
		}
	}
	// Lock both the persisted and requested scopes. A role-removal edit must
	// serialize with deletion of the old owner just as a role-addition edit does;
	// otherwise deletion cleanup could overwrite unrelated concurrent changes.
	if err := lockServiceAccountRoleScopeTransition(tx, currentScopes, account); err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE service_accounts SET
		name = ?, description = ?, owner_id = ?, contact_email = ?, active = ?,
		global_roles_json = ?, project_roles_json = ?, domain_roles_json = ?,
		repository_roles_json = ?, scopes_json = ?, updated_at = ?, expires_at = ?,
		rotation_interval_days = ?
		WHERE id = ? AND updated_at = ?`,
		account.Name,
		account.Description,
		account.OwnerID,
		account.ContactEmail,
		s.boolValue(account.Active),
		string(globalRolesJSON),
		string(projectRolesJSON),
		string(domainRolesJSON),
		string(repositoryRolesJSON),
		string(scopesJSON),
		account.UpdatedAt,
		account.ExpiresAt,
		account.RotationIntervalDays,
		account.ID,
		expectedUpdatedAt,
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) GetServiceAccount(id string) (*ServiceAccount, error) {
	return s.getServiceAccountByQuery(serviceAccountSelectColumns()+` FROM service_accounts WHERE id = ?`, id)
}

func (s *SQLiteStore) GetServiceAccountByKeyHash(keyHash string) (*ServiceAccount, error) {
	return s.getServiceAccountByQuery(serviceAccountSelectColumns()+` FROM service_accounts WHERE key_hash = ?`, keyHash)
}

func serviceAccountSelectColumns() string {
	return `SELECT id, name, description, COALESCE(owner_id, ''), COALESCE(contact_email, ''), key_hash, active, global_roles_json, project_roles_json, COALESCE(domain_roles_json, ''), COALESCE(repository_roles_json, ''), COALESCE(scopes_json, ''), created_at, updated_at, last_used_at, expires_at, key_rotated_at, rotation_interval_days`
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
		if err := rows.Scan(&account.ID, &account.Name, &account.Description, &account.OwnerID, &account.ContactEmail, &account.KeyHash, &active, &globalRolesJSON, &projectRolesJSON, &domainRolesJSON, &repositoryRolesJSON, &scopesJSON, &account.CreatedAt, &account.UpdatedAt, &lastUsedAt, &expiresAt, &keyRotatedAt, &rotationIntervalDays); err != nil {
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

// RotateServiceAccountKeyIfUnchanged guarantees that a key returned to the
// caller is the credential that won the rotation race. A stale rotation cannot
// silently succeed after a concurrent rotation, metadata update, use update, or
// deletion.
func (s *SQLiteStore) RotateServiceAccountKeyIfUnchanged(id, keyHash string, expectedUpdatedAt, updatedAt time.Time) (bool, error) {
	result, err := s.db.Exec(`UPDATE service_accounts SET key_hash = ?, updated_at = ?, key_rotated_at = ? WHERE id = ? AND updated_at = ?`,
		keyHash, updatedAt, updatedAt, id, expectedUpdatedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
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
	// Authentication telemetry is not an administrative account mutation and
	// must not invalidate metadata/key-rotation CAS snapshots. Throttle the
	// durable timestamp to avoid turning every authenticated request into a
	// control-plane write while still keeping last-used reporting current.
	const writeInterval = time.Minute
	_, err := s.db.Exec(`UPDATE service_accounts SET last_used_at = ?
		WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
		lastUsedAt, id, lastUsedAt.Add(-writeInterval))
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

func revokeGitHubIdentityKeysTx(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, revokedAt time.Time, keys ...string) error {
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := revokeGitHubConnectionTx(tx, key, revokedAt); err != nil {
			return err
		}
	}
	return nil
}

type authConnExecer struct {
	ctx  context.Context
	conn *sql.Conn
}

func (e authConnExecer) Exec(query string, args ...any) (sql.Result, error) {
	return e.conn.ExecContext(e.ctx, query, args...)
}

func (s *SQLiteStore) SaveUser(user *User) error {
	if user == nil {
		return fmt.Errorf("user is required")
	}
	user.Subject = strings.TrimSpace(user.Subject)
	if user.Subject == "" {
		return fmt.Errorf("user subject is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var currentSubject string
	lookupErr := tx.QueryRow(`SELECT subject FROM users WHERE id = ?`, user.ID).Scan(&currentSubject)
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return lookupErr
	}
	if errors.Is(lookupErr, sql.ErrNoRows) || strings.TrimSpace(currentSubject) != user.Subject {
		// Identity-keyed OAuth credentials must never transfer to a newly-created
		// principal or remain under a retired subject after rename. Fence all keys
		// in the same transaction as the user claim/update so an older callback
		// cannot repopulate them after this mutation returns.
		if err := revokeGitHubIdentityKeysTx(tx, time.Now().UTC(), user.ID, currentSubject, user.Subject); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO users (id, subject, email, display_name, active, created_at, updated_at, last_seen_at, disabled_at, session_epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			subject=excluded.subject,
			email=excluded.email,
			display_name=excluded.display_name,
			active=excluded.active,
			updated_at=excluded.updated_at,
			last_seen_at=excluded.last_seen_at,
			disabled_at=excluded.disabled_at,
			session_epoch=CASE WHEN users.subject <> excluded.subject
				THEN users.session_epoch + 1
				ELSE CASE WHEN users.session_epoch > excluded.session_epoch
					THEN users.session_epoch ELSE excluded.session_epoch END
			END`,
		user.ID,
		user.Subject,
		user.Email,
		user.DisplayName,
		s.boolValue(user.Active),
		user.CreatedAt,
		user.UpdatedAt,
		user.LastSeenAt,
		user.DisabledAt,
		user.SessionEpoch,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) UpdateUserIdentitySeen(id string, displayName string, email string, lastSeenAt time.Time, updatedAt time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("user id is required")
	}
	_, err := s.db.Exec(`UPDATE users
		SET display_name = ?, email = ?, last_seen_at = ?, updated_at = ?
		WHERE id = ?`,
		displayName, email, lastSeenAt, updatedAt, id)
	return err
}

func (s *SQLiteStore) SaveUserWithRoleBindings(user *User, bindings []*UserRoleBinding) error {
	if user == nil {
		return fmt.Errorf("user is required")
	}
	user.Subject = strings.TrimSpace(user.Subject)
	if user.Subject == "" {
		return fmt.Errorf("user subject is required")
	}
	user.ID = strings.TrimSpace(user.ID)
	if user.ID == "" {
		return fmt.Errorf("user id is required")
	}
	for _, binding := range bindings {
		if err := normalizeUserRoleBindingValues(binding); err != nil {
			return err
		}
		if binding.UserID != user.ID {
			return fmt.Errorf("user role binding owner %q does not match user %q", binding.UserID, user.ID)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	orderedBindings := append([]*UserRoleBinding(nil), bindings...)
	sort.Slice(orderedBindings, func(i, j int) bool {
		if orderedBindings[i] == nil {
			return orderedBindings[j] != nil
		}
		if orderedBindings[j] == nil {
			return false
		}
		left := strings.TrimSpace(orderedBindings[i].ScopeType) + "\x00" + strings.TrimSpace(orderedBindings[i].ScopeID)
		right := strings.TrimSpace(orderedBindings[j].ScopeType) + "\x00" + strings.TrimSpace(orderedBindings[j].ScopeID)
		return left < right
	})
	for _, binding := range orderedBindings {
		if err := normalizeAndLockUserRoleBindingScope(tx, binding); err != nil {
			return err
		}
	}
	var currentSubject string
	lookupErr := tx.QueryRow(`SELECT subject FROM users WHERE id = ?`, user.ID).Scan(&currentSubject)
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return lookupErr
	}
	if errors.Is(lookupErr, sql.ErrNoRows) || strings.TrimSpace(currentSubject) != user.Subject {
		if err := revokeGitHubIdentityKeysTx(tx, time.Now().UTC(), user.ID, currentSubject, user.Subject); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO users (id, subject, email, display_name, active, created_at, updated_at, last_seen_at, disabled_at, session_epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			subject=excluded.subject,
			email=excluded.email,
			display_name=excluded.display_name,
			active=excluded.active,
			updated_at=excluded.updated_at,
			last_seen_at=excluded.last_seen_at,
			disabled_at=excluded.disabled_at,
			session_epoch=CASE WHEN users.subject <> excluded.subject
				THEN users.session_epoch + 1
				ELSE CASE WHEN users.session_epoch > excluded.session_epoch
					THEN users.session_epoch ELSE excluded.session_epoch END
			END`,
		user.ID,
		user.Subject,
		user.Email,
		user.DisplayName,
		s.boolValue(user.Active),
		user.CreatedAt,
		user.UpdatedAt,
		user.LastSeenAt,
		user.DisabledAt,
		user.SessionEpoch,
	); err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding == nil {
			return fmt.Errorf("user role binding is required")
		}
		if _, err := tx.Exec(`INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at)
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
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BootstrapFirstAdmin atomically inserts the first user, its system-admin binding,
// and the bootstrap audit event only when the user table is still empty. The
// BEGIN IMMEDIATE lock closes the first-request race for SQLite/shared-DB
// controller deployments.
func (s *SQLiteStore) BootstrapFirstAdmin(user *User, binding *UserRoleBinding, event *AuditEvent) (bool, error) {
	if user == nil {
		return false, fmt.Errorf("user is required")
	}
	if binding == nil {
		return false, fmt.Errorf("user role binding is required")
	}
	if event == nil {
		return false, fmt.Errorf("audit event is required")
	}
	user.Subject = strings.TrimSpace(user.Subject)
	if user.Subject == "" {
		return false, fmt.Errorf("user subject is required")
	}
	user.ID = strings.TrimSpace(user.ID)
	if user.ID == "" {
		return false, fmt.Errorf("user id is required")
	}
	if err := normalizeUserRoleBindingValues(binding); err != nil {
		return false, err
	}
	if binding.UserID != user.ID {
		return false, fmt.Errorf("bootstrap role binding owner %q does not match user %q", binding.UserID, user.ID)
	}
	if binding.ScopeType != "global" || binding.Role != storageSystemAdminRole {
		return false, errors.New("bootstrap role binding must grant global system_admin")
	}
	detailsJSON, err := json.Marshal(event.Details)
	if err != nil {
		return false, err
	}
	if s.db.dialect != storeDialectSQLite {
		return s.bootstrapFirstAdminTx(user, binding, event, string(detailsJSON))
	}
	ctx := context.Background()
	conn, err := s.db.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return false, err
	}
	done := false
	defer func() {
		if !done {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		if _, err := conn.ExecContext(ctx, `ROLLBACK`); err != nil {
			return false, err
		}
		done = true
		return false, nil
	}
	if err := revokeGitHubIdentityKeysTx(authConnExecer{ctx: ctx, conn: conn}, time.Now().UTC(), user.ID, user.Subject); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO users (id, subject, email, display_name, active, created_at, updated_at, last_seen_at, disabled_at, session_epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID,
		user.Subject,
		user.Email,
		user.DisplayName,
		s.boolValue(user.Active),
		user.CreatedAt,
		user.UpdatedAt,
		user.LastSeenAt,
		user.DisabledAt,
		user.SessionEpoch,
	); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		binding.ID,
		binding.UserID,
		binding.ScopeType,
		binding.ScopeID,
		binding.Role,
		binding.CreatedAt,
		binding.UpdatedAt,
	); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO audit_events (id, action, actor_type, actor_subject, actor_display_name, project_id, domain_id, resource_type, resource_id, status, details_json, created_at)
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
	); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return false, err
	}
	done = true
	return true, nil
}

func (s *SQLiteStore) bootstrapFirstAdminTx(user *User, binding *UserRoleBinding, event *AuditEvent, detailsJSON string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if s.db.dialect == storeDialectPostgres {
		if _, err := tx.Exec(`LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
			return false, err
		}
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	if count != 0 {
		return false, nil
	}
	if err := revokeGitHubIdentityKeysTx(tx, time.Now().UTC(), user.ID, user.Subject); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO users (id, subject, email, display_name, active, created_at, updated_at, last_seen_at, disabled_at, session_epoch)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID,
		user.Subject,
		user.Email,
		user.DisplayName,
		s.boolValue(user.Active),
		user.CreatedAt,
		user.UpdatedAt,
		user.LastSeenAt,
		user.DisabledAt,
		user.SessionEpoch,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		binding.ID,
		binding.UserID,
		binding.ScopeType,
		binding.ScopeID,
		binding.Role,
		binding.CreatedAt,
		binding.UpdatedAt,
	); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO audit_events (id, action, actor_type, actor_subject, actor_display_name, project_id, domain_id, resource_type, resource_id, status, details_json, created_at)
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
		detailsJSON,
		event.CreatedAt,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
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
		query.WriteString(` AND (LOWER(subject) LIKE ? OR LOWER(email) LIKE ? OR LOWER(display_name) LIKE ? OR EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id
			  AND (LOWER(b.role) LIKE ? OR LOWER(b.scope_type) LIKE ? OR LOWER(COALESCE(b.scope_id, '')) LIKE ?)
		))`)
		like := "%" + strings.ToLower(q) + "%"
		args = append(args, like, like, like, like, like, like)
	}
	if filter.Active != nil {
		query.WriteString(` AND active = ?`)
		args = append(args, s.boolValue(*filter.Active))
	}
	if role := strings.ToLower(strings.TrimSpace(filter.Role)); role != "" {
		query.WriteString(` AND EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id AND LOWER(TRIM(b.role)) = ?
		)`)
		args = append(args, role)
	}
	if projectID := strings.TrimSpace(filter.ProjectID); projectID != "" {
		query.WriteString(` AND EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id AND LOWER(TRIM(b.scope_type)) = 'project' AND b.scope_id = ?
		)`)
		args = append(args, projectID)
	}
	if domainID := strings.TrimSpace(filter.DomainID); domainID != "" {
		query.WriteString(` AND EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id AND (
				(LOWER(TRIM(b.scope_type)) = 'domain' AND b.scope_id = ?)
				OR (LOWER(TRIM(b.scope_type)) = 'project' AND EXISTS (
					SELECT 1 FROM projects p WHERE p.id = b.scope_id AND p.domain_id = ?
				))
			)
		)`)
		args = append(args, domainID, domainID)
	}
	query.WriteString(` ORDER BY updated_at DESC LIMIT ? OFFSET ?`)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer would discard. The defer
	// covers the paths that never reach it — an early return or a panic in a
	// scan helper. sql.Rows.Close is documented idempotent, so both can run.
	defer rows.Close()
	users := []*User{}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, user := range users {
		bindings, err := s.ListUserRoleBindings(user.ID)
		if err != nil {
			return nil, err
		}
		user.Bindings = bindings
	}
	return users, nil
}

func (s *SQLiteStore) CountUsers(filter UserFilter) (int, error) {
	query := strings.Builder{}
	query.WriteString(`SELECT COUNT(*) FROM users WHERE 1 = 1`)
	args := []any{}
	if q := strings.TrimSpace(filter.Query); q != "" {
		query.WriteString(` AND (LOWER(subject) LIKE ? OR LOWER(email) LIKE ? OR LOWER(display_name) LIKE ? OR EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id
			  AND (LOWER(b.role) LIKE ? OR LOWER(b.scope_type) LIKE ? OR LOWER(COALESCE(b.scope_id, '')) LIKE ?)
		))`)
		like := "%" + strings.ToLower(q) + "%"
		args = append(args, like, like, like, like, like, like)
	}
	if filter.Active != nil {
		query.WriteString(` AND active = ?`)
		args = append(args, s.boolValue(*filter.Active))
	}
	if role := strings.ToLower(strings.TrimSpace(filter.Role)); role != "" {
		query.WriteString(` AND EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id AND LOWER(TRIM(b.role)) = ?
		)`)
		args = append(args, role)
	}
	if projectID := strings.TrimSpace(filter.ProjectID); projectID != "" {
		query.WriteString(` AND EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id AND LOWER(TRIM(b.scope_type)) = 'project' AND b.scope_id = ?
		)`)
		args = append(args, projectID)
	}
	if domainID := strings.TrimSpace(filter.DomainID); domainID != "" {
		query.WriteString(` AND EXISTS (
			SELECT 1 FROM user_role_bindings b
			WHERE b.user_id = users.id AND (
				(LOWER(TRIM(b.scope_type)) = 'domain' AND b.scope_id = ?)
				OR (LOWER(TRIM(b.scope_type)) = 'project' AND EXISTS (
					SELECT 1 FROM projects p WHERE p.id = b.scope_id AND p.domain_id = ?
				))
			)
		)`)
		args = append(args, domainID, domainID)
	}
	var count int
	if err := s.db.QueryRow(query.String(), args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) UpdateUserActive(id string, active bool, updatedAt time.Time) error {
	var disabledAt any
	if !active {
		disabledAt = updatedAt
	}
	_, err := s.db.Exec(`UPDATE users SET active = ?, updated_at = ?, disabled_at = ? WHERE id = ?`, s.boolValue(active), updatedAt, disabledAt, id)
	return err
}

type authMutationTx interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

type authConnMutationTx struct {
	ctx     context.Context
	conn    *sql.Conn
	dialect storeDialect
}

func (tx authConnMutationTx) Exec(query string, args ...any) (sql.Result, error) {
	return tx.conn.ExecContext(tx.ctx, rewriteSQL(query, tx.dialect), args...)
}

func (tx authConnMutationTx) QueryRow(query string, args ...any) *sql.Row {
	return tx.conn.QueryRowContext(tx.ctx, rewriteSQL(query, tx.dialect), args...)
}

func (s *SQLiteStore) withAuthMutationLock(fn func(authMutationTx) (bool, error)) (bool, error) {
	if s.db.dialect == storeDialectSQLite {
		ctx := context.Background()
		conn, err := s.db.db.Conn(ctx)
		if err != nil {
			return false, err
		}
		defer conn.Close()
		deadline := time.Now().Add(2 * time.Second)
		for {
			if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "database is locked") && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
					continue
				}
				return false, err
			}
			break
		}
		done := false
		defer func() {
			if !done {
				_, _ = conn.ExecContext(ctx, `ROLLBACK`)
			}
		}()
		ok, err := fn(authConnMutationTx{ctx: ctx, conn: conn, dialect: s.db.dialect})
		if err != nil {
			return false, err
		}
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return false, err
		}
		done = true
		return ok, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if s.db.dialect == storeDialectPostgres {
		// OAuth completion takes the revocation lifecycle row before writing the
		// connection. Take table locks in that same order so offboarding/rename
		// cannot deadlock with an in-flight callback (revocation row -> connection
		// versus connection table -> revocation row).
		if _, err := tx.Exec(`LOCK TABLE github_connection_revocations, github_connections, users, user_role_bindings IN EXCLUSIVE MODE`); err != nil {
			return false, err
		}
	}
	ok, err := fn(tx)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return ok, nil
}

func (s *SQLiteStore) authMutationTargetIsActiveGlobalAdmin(tx authMutationTx, userID string) (bool, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(*) FROM users u
		JOIN user_role_bindings b ON b.user_id = u.id
		WHERE u.id = ? AND u.active = ?
		  AND LOWER(TRIM(b.scope_type)) = 'global'
		  AND LOWER(TRIM(b.role)) = ?`,
		userID, s.boolValue(true), storageSystemAdminRole).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *SQLiteStore) authMutationActiveGlobalAdminCount(tx authMutationTx) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(DISTINCT u.id) FROM users u
		JOIN user_role_bindings b ON b.user_id = u.id
		WHERE u.active = ?
		  AND LOWER(TRIM(b.scope_type)) = 'global'
		  AND LOWER(TRIM(b.role)) = ?`,
		s.boolValue(true), storageSystemAdminRole).Scan(&count)
	return count, err
}

func (s *SQLiteStore) UpdateUserActiveGuardLastAdmin(id string, active bool, updatedAt time.Time) (bool, error) {
	if active {
		return true, s.UpdateUserActive(id, true, updatedAt)
	}
	return s.withAuthMutationLock(func(tx authMutationTx) (bool, error) {
		isAdmin, err := s.authMutationTargetIsActiveGlobalAdmin(tx, id)
		if err != nil {
			return false, err
		}
		if isAdmin {
			count, err := s.authMutationActiveGlobalAdminCount(tx)
			if err != nil {
				return false, err
			}
			if count <= 1 {
				return false, nil
			}
		}
		_, err = tx.Exec(`UPDATE users SET active = ?, updated_at = ?, disabled_at = ? WHERE id = ?`,
			s.boolValue(false), updatedAt, updatedAt, id)
		return err == nil, err
	})
}

// UpdateUserGuardLastAdmin updates only administrator-editable fields under the
// same serialization lock used by role/deletion mutations. It intentionally
// leaves last_seen_at and session_epoch untouched so a stale profile request
// cannot undo a concurrent login observation or session revocation.
func (s *SQLiteStore) UpdateUserGuardLastAdmin(user *User) (bool, error) {
	return s.updateUserGuardLastAdmin(user, nil)
}

// ErrUserConcurrentChange reports that administrator-owned user fields changed
// since the caller loaded its snapshot. Session-epoch/last-seen-only maintenance
// is deliberately excluded from this comparison.
var ErrUserConcurrentChange = errors.New("user changed concurrently")

// UpdateUserGuardLastAdminIfUnchanged adds optimistic concurrency to the guarded
// administrator update without conflicting with concurrent last-seen/session
// maintenance that leaves identity/profile fields unchanged.
func (s *SQLiteStore) UpdateUserGuardLastAdminIfUnchanged(user *User, expected *User) (bool, error) {
	if expected == nil || user == nil || strings.TrimSpace(expected.ID) == "" || expected.ID != user.ID {
		return false, fmt.Errorf("matching current and expected users are required")
	}
	return s.updateUserGuardLastAdmin(user, expected)
}

func (s *SQLiteStore) updateUserGuardLastAdmin(user *User, expected *User) (bool, error) {
	if user == nil {
		return false, fmt.Errorf("user is required")
	}
	user.Subject = strings.TrimSpace(user.Subject)
	if user.Subject == "" {
		return false, fmt.Errorf("user subject is required")
	}
	return s.withAuthMutationLock(func(tx authMutationTx) (bool, error) {
		var currentSubject, currentEmail, currentDisplayName string
		var currentActive any
		var currentDisabledAt sql.NullTime
		if err := tx.QueryRow(`SELECT subject, COALESCE(email, ''), COALESCE(display_name, ''), active, disabled_at FROM users WHERE id = ?`, user.ID).Scan(&currentSubject, &currentEmail, &currentDisplayName, &currentActive, &currentDisabledAt); err != nil {
			if err == sql.ErrNoRows {
				return false, nil
			}
			return false, err
		}
		if expected != nil && (strings.TrimSpace(currentSubject) != strings.TrimSpace(expected.Subject) ||
			currentEmail != expected.Email || currentDisplayName != expected.DisplayName ||
			sqlBool(currentActive) != expected.Active || !nullableTimeMatches(currentDisabledAt, expected.DisabledAt)) {
			return false, ErrUserConcurrentChange
		}
		isAdmin, err := s.authMutationTargetIsActiveGlobalAdmin(tx, user.ID)
		if err != nil {
			return false, err
		}
		makesAdminUnreachable := !user.Active || user.Subject != strings.TrimSpace(currentSubject)
		if isAdmin && sqlBool(currentActive) && makesAdminUnreachable {
			count, err := s.authMutationActiveGlobalAdminCount(tx)
			if err != nil {
				return false, err
			}
			if count <= 1 {
				return false, nil
			}
		}
		if strings.TrimSpace(currentSubject) != user.Subject {
			if err := revokeGitHubIdentityKeysTx(tx, time.Now().UTC(), user.ID, currentSubject, user.Subject); err != nil {
				return false, err
			}
		}
		result, err := tx.Exec(`UPDATE users SET subject = ?, email = ?, display_name = ?, active = ?, updated_at = ?, disabled_at = ?,
			session_epoch = session_epoch + CASE WHEN subject <> ? THEN 1 ELSE 0 END WHERE id = ?`,
			user.Subject, user.Email, user.DisplayName, s.boolValue(user.Active), user.UpdatedAt, user.DisabledAt, user.Subject, user.ID)
		if err != nil {
			return false, err
		}
		rows, err := result.RowsAffected()
		return rows == 1, err
	})
}

func nullableTimeMatches(current sql.NullTime, expected *time.Time) bool {
	if !current.Valid || expected == nil {
		return !current.Valid && expected == nil
	}
	return current.Time.Equal(*expected)
}

func (s *SQLiteStore) CountActiveGlobalRoleHolders(role string) (int, error) {
	normalized := strings.ToLower(strings.TrimSpace(role))
	if normalized == "" {
		return 0, nil
	}
	var count int
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT u.id) FROM users u
		JOIN user_role_bindings b ON b.user_id = u.id
		WHERE u.active = ?
		  AND LOWER(TRIM(b.scope_type)) = 'global'
		  AND LOWER(TRIM(b.role)) = ?`, s.boolValue(true), normalized).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *SQLiteStore) BumpUserSessionEpoch(id string, updatedAt time.Time) (int, error) {
	if _, err := s.db.Exec(`UPDATE users SET session_epoch = session_epoch + 1, updated_at = ? WHERE id = ?`, updatedAt.UTC(), id); err != nil {
		return 0, err
	}
	var epoch sql.NullInt64
	if err := s.db.QueryRow(`SELECT COALESCE(session_epoch, 0) FROM users WHERE id = ?`, id).Scan(&epoch); err != nil {
		return 0, err
	}
	return int(epoch.Int64), nil
}

func (s *SQLiteStore) DeleteUser(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("user id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Pair with SaveUserRoleBinding's owner-row lock before deleting child
	// bindings. Without this ordering PostgreSQL could take the child snapshot,
	// miss an uncommitted concurrent insert, and then leave that grant orphaned
	// after the user delete commits (its compatibility schema has no FK cascade).
	if _, err := tx.Exec(`UPDATE users SET updated_at = updated_at WHERE id = ?`, id); err != nil {
		return err
	}
	var subject string
	if err := tx.QueryRow(`SELECT subject FROM users WHERE id = ?`, id).Scan(&subject); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := revokeGitHubIdentityKeysTx(tx, time.Now().UTC(), id, subject); err != nil {
		return err
	}
	// Delete role bindings explicitly for backend parity. SQLite enforces the
	// declared ON DELETE CASCADE, while the Postgres compatibility rewriter strips
	// schema-level foreign keys; the explicit delete keeps both paths safe if a
	// deleted user ID is later reused.
	if _, err := tx.Exec(`DELETE FROM user_role_bindings WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) DeleteUserGuardLastAdmin(id string) (bool, error) {
	return s.withAuthMutationLock(func(tx authMutationTx) (bool, error) {
		isAdmin, err := s.authMutationTargetIsActiveGlobalAdmin(tx, id)
		if err != nil {
			return false, err
		}
		if isAdmin {
			count, err := s.authMutationActiveGlobalAdminCount(tx)
			if err != nil {
				return false, err
			}
			if count <= 1 {
				return false, nil
			}
		}
		var subject string
		if err := tx.QueryRow(`SELECT subject FROM users WHERE id = ?`, id).Scan(&subject); err != nil && err != sql.ErrNoRows {
			return false, err
		}
		revokedAt := time.Now().UTC()
		if err := revokeGitHubConnectionTx(tx, id, revokedAt); err != nil {
			return false, err
		}
		if strings.TrimSpace(subject) != "" && strings.TrimSpace(subject) != id {
			if err := revokeGitHubConnectionTx(tx, strings.TrimSpace(subject), revokedAt); err != nil {
				return false, err
			}
		}
		if _, err := tx.Exec(`DELETE FROM user_role_bindings WHERE user_id = ?`, id); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *SQLiteStore) SaveUserRoleBinding(binding *UserRoleBinding) error {
	if binding == nil {
		return fmt.Errorf("user role binding is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := normalizeAndLockUserRoleBindingScope(tx, binding); err != nil {
		return err
	}
	if err := lockUserRoleBindingOwner(tx, binding); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO user_role_bindings (id, user_id, scope_type, scope_id, role, created_at, updated_at)
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
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) DeleteUserRoleBinding(id string) error {
	_, err := s.db.Exec(`DELETE FROM user_role_bindings WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) DeleteUserRoleBindingGuardLastAdmin(id string) (bool, error) {
	return s.withAuthMutationLock(func(tx authMutationTx) (bool, error) {
		var userID, scopeType, role string
		var active any
		err := tx.QueryRow(`SELECT b.user_id, b.scope_type, b.role, u.active
			FROM user_role_bindings b
			JOIN users u ON u.id = b.user_id
			WHERE b.id = ?`, id).Scan(&userID, &scopeType, &role, &active)
		if err != nil {
			if err == sql.ErrNoRows {
				return true, nil
			}
			return false, err
		}
		if sqlBool(active) && strings.TrimSpace(scopeType) == "global" && strings.EqualFold(strings.TrimSpace(role), storageSystemAdminRole) {
			count, err := s.authMutationActiveGlobalAdminCount(tx)
			if err != nil {
				return false, err
			}
			if count <= 1 {
				return false, nil
			}
		}
		_, err = tx.Exec(`DELETE FROM user_role_bindings WHERE id = ?`, id)
		return err == nil, err
	})
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
	return `SELECT id, subject, COALESCE(email, ''), COALESCE(display_name, ''), active, created_at, updated_at, last_seen_at, disabled_at, COALESCE(session_epoch, 0)`
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
	var sessionEpoch sql.NullInt64
	if err := scanner.Scan(&user.ID, &user.Subject, &user.Email, &user.DisplayName, &active, &user.CreatedAt, &user.UpdatedAt, &lastSeenAt, &disabledAt, &sessionEpoch); err != nil {
		return nil, err
	}
	user.Active = sqlBool(active)
	if sessionEpoch.Valid {
		user.SessionEpoch = int(sessionEpoch.Int64)
	}
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

func (s *SQLiteStore) DeleteAuditEventsBefore(cutoff time.Time) (int64, error) {
	if _, err := s.db.Exec(`DELETE FROM object_activity_events WHERE created_at < ?`, cutoff.UTC()); err != nil {
		return 0, err
	}
	result, err := s.db.Exec(`DELETE FROM audit_events WHERE created_at < ?`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *SQLiteStore) ListAuditEvents(filter AuditEventFilter) ([]*AuditEvent, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	where, args := buildAuditEventFilterWhere(filter)
	query := strings.Builder{}
	query.WriteString(`SELECT id, action, actor_type, actor_subject, actor_display_name, COALESCE(project_id, ''), COALESCE(domain_id, ''), resource_type, resource_id, status, details_json, created_at FROM audit_events`)
	query.WriteString(where)
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`)
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

func (s *SQLiteStore) CountAuditEvents(filter AuditEventFilter) (int, error) {
	where, args := buildAuditEventFilterWhere(filter)
	query := `SELECT COUNT(*) FROM audit_events` + where
	var count int
	if err := s.db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func buildAuditEventFilterWhere(filter AuditEventFilter) (string, []any) {
	query := strings.Builder{}
	query.WriteString(` WHERE 1 = 1`)
	args := make([]any, 0, 8)
	appendFilter := func(column string, value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		query.WriteString(` AND ` + column + ` = ?`)
		args = append(args, trimmed)
	}
	if filter.FleetOnly {
		query.WriteString(` AND (action LIKE 'fleet.%' OR action LIKE 'generator.%' OR action LIKE 'provisioning.%' OR resource_type IN ('fleet_alert', 'fleet_pool', 'generator', 'provisioning_request', 'provisioning_operation'))`)
	}
	appendFilter("action", filter.Action)
	appendFilter("actor_type", filter.ActorType)
	appendFilter("actor_subject", filter.ActorSubject)
	appendFilter("project_id", filter.ProjectID)
	appendFilter("domain_id", filter.DomainID)
	appendFilter("resource_type", filter.ResourceType)
	appendFilter("resource_id", filter.ResourceID)
	appendFilter("status", filter.Status)
	if filter.ProjectIDs != nil {
		placeholders := make([]string, 0, len(filter.ProjectIDs))
		for _, id := range filter.ProjectIDs {
			trimmed := strings.TrimSpace(id)
			if trimmed == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, trimmed)
		}
		if len(placeholders) == 0 {
			// A non-nil-but-all-blank allow-list means "no authorized projects":
			// match nothing rather than degrading to an unfiltered read.
			query.WriteString(` AND 1 = 0`)
		} else {
			query.WriteString(` AND project_id IN (` + strings.Join(placeholders, ", ") + `)`)
		}
	}
	if filter.CreatedAfter != nil {
		query.WriteString(` AND created_at >= ?`)
		args = append(args, *filter.CreatedAfter)
	}
	if filter.CreatedBefore != nil {
		query.WriteString(` AND created_at <= ?`)
		args = append(args, *filter.CreatedBefore)
	}
	return query.String(), args
}
