package api

// auth_resolution.go — split from middleware.go (same package, behavior-preserving).
// Request authentication: identity/context resolution, local bindings, API-key and
// service-account auth, role-header parsing, and AuthenticatedUser role-check methods.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func AuthenticatedUserFromContext(ctx context.Context) (*AuthenticatedUser, bool) {
	user, ok := ctx.Value(authUserContextKey{}).(*AuthenticatedUser)
	return user, ok
}

func AuthenticatedServiceAccountFromContext(ctx context.Context) (*AuthenticatedServiceAccount, bool) {
	account, ok := ctx.Value(authServiceAccountContextKey{}).(*AuthenticatedServiceAccount)
	return account, ok
}

func withAuthenticatedContext(r *http.Request, resolution authResolution) *http.Request {
	ctx := r.Context()
	if resolution.User != nil {
		ctx = context.WithValue(ctx, authUserContextKey{}, resolution.User)
	}
	if resolution.ServiceAccount != nil {
		ctx = context.WithValue(ctx, authServiceAccountContextKey{}, resolution.ServiceAccount)
	}
	return r.WithContext(ctx)
}

func resolveRequestAuth(r *http.Request, settings AuthSettings, store storage.Store) (authResolution, error) {
	if account, user, ok, err := authenticatedUserFromAPIKey(r, store); err != nil {
		return authResolution{}, err
	} else if ok {
		return authResolution{User: user, ServiceAccount: account}, nil
	}
	if settings.Mode == authModeOIDC {
		user, err := authenticatedUserFromOIDCSession(r, settings)
		if err != nil {
			return authResolution{}, err
		}
		localUser, err := applyLocalUserBindings(user, store)
		if err != nil {
			return authResolution{}, err
		}
		return authResolution{User: localUser}, nil
	}
	user, err := authenticatedUserFromHeaders(r, settings)
	if err != nil {
		return authResolution{}, err
	}
	localUser, err := applyLocalUserBindings(user, store)
	if err != nil {
		return authResolution{}, err
	}
	return authResolution{User: localUser}, nil
}

func applyLocalUserBindings(upstream *AuthenticatedUser, store storage.Store) (*AuthenticatedUser, error) {
	if upstream == nil {
		return nil, fmt.Errorf("authentication required")
	}
	if store == nil {
		return upstream, nil
	}
	local, err := store.GetUserBySubject(upstream.Subject)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			users, listErr := store.ListUsers(storage.UserFilter{Limit: 1})
			if listErr == nil && len(users) == 0 {
				return upstream, nil
			}
			return nil, fmt.Errorf("user is not onboarded")
		}
		return nil, fmt.Errorf("user access lookup failed")
	}
	if local == nil || !local.Active {
		return nil, fmt.Errorf("user is disabled")
	}
	now := time.Now().UTC()
	local.LastSeenAt = &now
	local.DisplayName = firstNonEmpty(upstream.DisplayName, local.DisplayName)
	local.Email = firstNonEmpty(upstream.Email, local.Email)
	local.UpdatedAt = now
	_ = store.SaveUser(local)
	user := &AuthenticatedUser{
		ID:              local.ID,
		Subject:         local.Subject,
		DisplayName:     firstNonEmpty(local.DisplayName, upstream.DisplayName),
		Email:           firstNonEmpty(local.Email, upstream.Email),
		GlobalRoles:     make(map[string]struct{}),
		ProjectRoles:    make(map[string]map[string]struct{}),
		DomainRoles:     make(map[string]map[string]struct{}),
		RepositoryRoles: make(map[string]map[string]struct{}),
	}
	for _, binding := range local.Bindings {
		if binding == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(binding.Role))
		if role == "" {
			continue
		}
		switch strings.TrimSpace(binding.ScopeType) {
		case "global":
			user.GlobalRoles[role] = struct{}{}
		case "project":
			addScopedUserRole(user.ProjectRoles, normalizeProjectID(binding.ScopeID), role)
		case "domain":
			addScopedUserRole(user.DomainRoles, normalizeDomainID(binding.ScopeID), role)
		case "application_repository":
			addScopedUserRole(user.RepositoryRoles, strings.TrimSpace(binding.ScopeID), role)
		}
	}
	return user, nil
}

func addScopedUserRole(scoped map[string]map[string]struct{}, scopeID string, role string) {
	if scopeID == "" || role == "" {
		return
	}
	if scoped[scopeID] == nil {
		scoped[scopeID] = map[string]struct{}{}
	}
	scoped[scopeID][role] = struct{}{}
}

func authenticatedUserFromAPIKey(r *http.Request, store storage.Store) (*AuthenticatedServiceAccount, *AuthenticatedUser, bool, error) {
	if store == nil {
		return nil, nil, false, nil
	}
	key := extractAPIKey(r)
	if key == "" {
		return nil, nil, false, nil
	}
	account, err := store.GetServiceAccountByKeyHash(hashAPIKey(key))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, false, fmt.Errorf("authentication required")
		}
		return nil, nil, false, fmt.Errorf("authentication required")
	}
	usedAt := time.Now().UTC()
	if err := store.UpdateServiceAccountLastUsed(account.ID, usedAt); err == nil {
		account.LastUsedAt = &usedAt
	}
	user := authenticatedUserFromServiceAccount(account)
	return &AuthenticatedServiceAccount{ID: account.ID, Name: account.Name, Description: account.Description, Scopes: normalizeAuthenticatedServiceAccountScopes(account.Scopes)}, user, true, nil
}

func extractAPIKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get("X-APEXLoad-API-Key"))
	if key != "" {
		return key
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization == "" {
		return ""
	}
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func hashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return fmt.Sprintf("%x", sum[:])
}

func serviceAccountAllowsRequest(account *AuthenticatedServiceAccount, r *http.Request) bool {
	if account == nil {
		return true
	}
	required := requiredServiceAccountScope(r)
	if required == "" {
		return true
	}
	scopes := normalizeAuthenticatedServiceAccountScopes(account.Scopes)
	for _, scope := range scopes {
		if scope == "*" || scope == "automation:*" || scope == required {
			return true
		}
		if required == "read" && (scope == "results:read" || scope == "system:read" || scope == "audit:read") {
			return true
		}
		if required == "write" && (scope == "scripts:write" || scope == "tests:run" || scope == "results:write" || scope == "settings:manage" || scope == "service_accounts:manage") {
			return true
		}
	}
	return false
}

func requiredServiceAccountScope(r *http.Request) string {
	path := r.URL.Path
	method := r.Method
	if strings.HasPrefix(path, "/api/v1/auth/service-accounts") {
		return "service_accounts:manage"
	}
	if strings.HasPrefix(path, "/api/v1/auth/users") {
		return "service_accounts:manage"
	}
	if strings.HasPrefix(path, "/api/v1/auth/user-activity") {
		return "audit:read"
	}
	if strings.HasPrefix(path, "/api/v1/auth/audit-events") {
		return "audit:read"
	}
	if path == "/api/v1/system/settings" && method != http.MethodGet {
		return "settings:manage"
	}
	if path == "/api/v1/system/generators/drain" && method == http.MethodPatch {
		return "settings:manage"
	}
	if strings.HasPrefix(path, "/api/v1/system/restarts") {
		if method == http.MethodGet {
			return "settings:manage"
		}
		return "settings:manage"
	}
	if strings.HasPrefix(path, "/api/v1/system/") {
		return "system:read"
	}
	if strings.HasPrefix(path, "/api/v1/results") {
		if method == http.MethodGet || method == http.MethodHead {
			return "results:read"
		}
		return "results:write"
	}
	if strings.HasPrefix(path, "/api/v1/library") {
		if method == http.MethodGet || method == http.MethodHead || path == "/api/v1/library/export" || strings.HasSuffix(path, "/diff") {
			return "read"
		}
		if path == "/api/v1/library/import/archive" {
			return "results:write"
		}
		if path == "/api/v1/library/import" {
			return requiredLibraryImportServiceAccountScope(r)
		}
		if path == "/api/v1/library/remotes" || strings.HasSuffix(path, "/probe") || method == http.MethodPut || method == http.MethodDelete {
			return "settings:manage"
		}
		if strings.HasSuffix(path, "/pull") || strings.HasSuffix(path, "/push") {
			return requiredLibraryTransferServiceAccountScope(r)
		}
		return "write"
	}
	if strings.HasPrefix(path, "/api/v1/remotes/") {
		if method == http.MethodGet || method == http.MethodHead {
			return "results:read"
		}
		return "tests:run"
	}
	if strings.HasPrefix(path, "/api/v1/scripts") {
		if method == http.MethodGet || method == http.MethodHead {
			return "read"
		}
		return "scripts:write"
	}
	if strings.HasPrefix(path, "/api/v1/tests") || strings.HasPrefix(path, "/api/v1/scenarios") {
		if method == http.MethodGet || method == http.MethodHead {
			return "results:read"
		}
		return "tests:run"
	}
	if method == http.MethodGet || method == http.MethodHead {
		return "read"
	}
	return "write"
}

func requiredLibraryTransferServiceAccountScope(r *http.Request) string {
	var req LibraryTransferRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "write"
	}
	return serviceAccountScopeForLibraryKind(req.Kind)
}

func requiredLibraryImportServiceAccountScope(r *http.Request) string {
	var req LibraryImportRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "write"
	}
	scope := ""
	for _, item := range req.Bundle.Items {
		next := serviceAccountScopeForLibraryKind(item.Kind)
		if next == "results:write" {
			return next
		}
		if next == "tests:run" {
			scope = next
		} else if scope == "" {
			scope = next
		}
	}
	return firstNonEmpty(scope, "write")
}

func serviceAccountScopeForLibraryKind(kind string) string {
	switch normalizeLibraryKind(kind) {
	case "script":
		return "scripts:write"
	case "scenario":
		return "tests:run"
	case "result":
		return "results:write"
	default:
		return "write"
	}
}

func normalizeAuthenticatedServiceAccountScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{"automation:*"}
	}
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return []string{"automation:*"}
	}
	return out
}

func authenticatedUserFromServiceAccount(account *storage.ServiceAccount) *AuthenticatedUser {
	user := &AuthenticatedUser{
		Subject:         "service-account:" + account.ID,
		DisplayName:     account.Name,
		GlobalRoles:     make(map[string]struct{}),
		ProjectRoles:    make(map[string]map[string]struct{}),
		DomainRoles:     make(map[string]map[string]struct{}),
		RepositoryRoles: make(map[string]map[string]struct{}),
	}
	for _, role := range account.GlobalRoles {
		trimmed := strings.ToLower(strings.TrimSpace(role))
		if trimmed != "" {
			user.GlobalRoles[trimmed] = struct{}{}
		}
	}
	for projectID, roles := range account.ProjectRoles {
		normalizedProjectID := normalizeProjectID(projectID)
		if normalizedProjectID == "" {
			continue
		}
		roleSet := make(map[string]struct{})
		for _, role := range roles {
			trimmed := strings.ToLower(strings.TrimSpace(role))
			if trimmed != "" {
				roleSet[trimmed] = struct{}{}
			}
		}
		if len(roleSet) > 0 {
			user.ProjectRoles[normalizedProjectID] = roleSet
		}
	}
	for domainID, roles := range account.DomainRoles {
		normalizedDomainID := normalizeDomainID(domainID)
		if normalizedDomainID == "" {
			continue
		}
		roleSet := rolesToSet(roles)
		if len(roleSet) > 0 {
			user.DomainRoles[normalizedDomainID] = roleSet
		}
	}
	for repositoryID, roles := range account.RepositoryRoles {
		normalizedRepositoryID := strings.TrimSpace(repositoryID)
		if normalizedRepositoryID == "" {
			continue
		}
		roleSet := rolesToSet(roles)
		if len(roleSet) > 0 {
			user.RepositoryRoles[normalizedRepositoryID] = roleSet
		}
	}
	return user
}

func authenticatedUserFromHeaders(r *http.Request, settings AuthSettings) (*AuthenticatedUser, error) {
	subject := strings.TrimSpace(r.Header.Get(settings.UserHeader))
	if subject == "" {
		return nil, fmt.Errorf("authentication required")
	}

	return &AuthenticatedUser{
		Subject:         subject,
		DisplayName:     strings.TrimSpace(r.Header.Get(settings.NameHeader)),
		Email:           strings.TrimSpace(r.Header.Get(settings.EmailHeader)),
		GlobalRoles:     parseRoleHeader(r.Header.Get(settings.RolesHeader)),
		ProjectRoles:    parseScopedRolesHeader(r.Header.Get(settings.ProjectRolesHeader), normalizeProjectID),
		DomainRoles:     parseScopedRolesHeader(r.Header.Get(settings.DomainRolesHeader), normalizeDomainID),
		RepositoryRoles: parseScopedRolesHeader(r.Header.Get(settings.RepositoryRolesHeader), strings.TrimSpace),
	}, nil
}

func (u *AuthenticatedUser) hasGlobalRole(roles ...string) bool {
	for _, role := range roles {
		if _, ok := u.GlobalRoles[role]; ok {
			return true
		}
	}
	return false
}

func (u *AuthenticatedUser) hasProjectRole(projectID string, roles ...string) bool {
	if projectID == "" {
		return false
	}
	projectRoles, ok := u.ProjectRoles[normalizeProjectID(projectID)]
	if !ok {
		return false
	}
	for _, role := range roles {
		if _, ok := projectRoles[role]; ok {
			return true
		}
	}
	return false
}

func (u *AuthenticatedUser) hasDomainRole(domainID string, roles ...string) bool {
	if domainID == "" {
		return false
	}
	domainRoles, ok := u.DomainRoles[normalizeDomainID(domainID)]
	if !ok {
		return false
	}
	for _, role := range roles {
		if _, ok := domainRoles[role]; ok {
			return true
		}
	}
	return false
}

func (u *AuthenticatedUser) hasRepositoryRole(repositoryID string, roles ...string) bool {
	if strings.TrimSpace(repositoryID) == "" {
		return false
	}
	repositoryRoles, ok := u.RepositoryRoles[strings.TrimSpace(repositoryID)]
	if !ok {
		return false
	}
	for _, role := range roles {
		if _, ok := repositoryRoles[role]; ok {
			return true
		}
	}
	return false
}

func parseRoleHeader(value string) map[string]struct{} {
	roles := make(map[string]struct{})
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '|', ' ', '\t', '\n':
			return true
		default:
			return false
		}
	}) {
		role := strings.ToLower(strings.TrimSpace(token))
		if role == "" {
			continue
		}
		roles[role] = struct{}{}
	}
	return roles
}

func parseProjectRolesHeader(value string) map[string]map[string]struct{} {
	return parseScopedRolesHeader(value, normalizeProjectID)
}

func parseScopedRolesHeader(value string, normalizeScope func(string) string) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{})
	for _, assignment := range strings.Split(value, ";") {
		assignment = strings.TrimSpace(assignment)
		if assignment == "" {
			continue
		}
		parts := strings.SplitN(assignment, "=", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(assignment, ":", 2)
		}
		if len(parts) != 2 {
			continue
		}
		scopeID := normalizeScope(strings.TrimSpace(parts[0]))
		roles := parseRoleHeader(parts[1])
		if scopeID == "" || len(roles) == 0 {
			continue
		}
		out[scopeID] = roles
	}
	return out
}

func rolesToSet(roles []string) map[string]struct{} {
	roleSet := make(map[string]struct{})
	for _, role := range roles {
		trimmed := strings.ToLower(strings.TrimSpace(role))
		if trimmed != "" {
			roleSet[trimmed] = struct{}{}
		}
	}
	return roleSet
}
