package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	RoleSystemAdmin  = "system_admin"
	RoleProjectAdmin = "project_admin"
	RoleEditor       = "editor"
	RoleViewer       = "viewer"
)

type authUserContextKey struct{}
type authServiceAccountContextKey struct{}

type AuthenticatedUser struct {
	ID              string
	Subject         string
	DisplayName     string
	Email           string
	GlobalRoles     map[string]struct{}
	ProjectRoles    map[string]map[string]struct{}
	DomainRoles     map[string]map[string]struct{}
	RepositoryRoles map[string]map[string]struct{}
}

type AuthenticatedServiceAccount struct {
	ID          string
	Name        string
	Description string
	Scopes      []string
}

type accessRequirement struct {
	AuthenticatedOnly bool
	GlobalRoles       []string
	ProjectRoles      []string
	ProjectLookup     func(*http.Request, storage.Store) (string, int, string)
	DomainRoles       []string
	DomainLookup      func(*http.Request, storage.Store) (string, int, string)
	RepositoryRoles   []string
	RepositoryLookup  func(*http.Request, storage.Store) (string, int, string)
}

type requestProjectLookup func(*http.Request) (string, int, string)

type requestCoordinationStore interface {
	RateLimitStore
	IdempotencyStore
}

type authResolution struct {
	User           *AuthenticatedUser
	ServiceAccount *AuthenticatedServiceAccount
}

func authMiddleware(settings func() AuthSettings, store storage.Store, coordination requestCoordinationStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
				next.ServeHTTP(w, r)
				return
			}

			requirement, ok := accessRequirementForRequest(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}

			current := normalizeAuthSettings(settings())
			if resolution, _, ok := devPreviewResolutionForRequest(current, r); ok {
				if authorizeAccessRequirement(w, r, store, resolution, requirement) {
					next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
				}
				return
			}
			if isAuthBypassMode(current.Mode) {
				next.ServeHTTP(w, r)
				return
			}

			resolution, err := resolveRequestAuth(r, current, store)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}
			if !enforceServiceAccountRateLimit(w, r, coordination, resolution.ServiceAccount) {
				return
			}
			if authorizeAccessRequirement(w, r, store, resolution, requirement) {
				if !enforceIdempotencyKey(w, r, coordination, resolution) {
					return
				}
				next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
			}
		})
	}
}

func projectScopedAuthMiddleware(settings func() AuthSettings, store storage.Store, coordination requestCoordinationStore, globalRoles []string, projectRoles []string, projectLookup requestProjectLookup) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current := normalizeAuthSettings(settings())
			requirement := accessRequirement{
				GlobalRoles:  globalRoles,
				ProjectRoles: projectRoles,
			}
			if projectLookup != nil {
				requirement.ProjectLookup = func(r *http.Request, _ storage.Store) (string, int, string) {
					return projectLookup(r)
				}
			}
			if resolution, _, ok := devPreviewResolutionForRequest(current, r); ok {
				if authorizeAccessRequirement(w, r, store, resolution, requirement) {
					next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
				}
				return
			}
			if isAuthBypassMode(current.Mode) {
				next.ServeHTTP(w, r)
				return
			}

			resolution, err := resolveRequestAuth(r, current, store)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}
			if !enforceServiceAccountRateLimit(w, r, coordination, resolution.ServiceAccount) {
				return
			}
			if authorizeAccessRequirement(w, r, store, resolution, requirement) {
				if !enforceIdempotencyKey(w, r, coordination, resolution) {
					return
				}
				next.ServeHTTP(w, withAuthenticatedContext(r, resolution))
			}
		})
	}
}

func enforceServiceAccountRateLimit(w http.ResponseWriter, r *http.Request, store RateLimitStore, account *AuthenticatedServiceAccount) bool {
	if store == nil || account == nil {
		return true
	}
	limit := configuredAPIKeyRateLimitPerMinute()
	if limit <= 0 {
		return true
	}
	allowed, remaining, err := store.AllowRateLimit(r.Context(), "service-account:"+account.ID, limit, time.Minute)
	if err != nil {
		slog.Warn("service account rate-limit check failed", "service_account_id", account.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "service account rate-limit store unavailable")
		return false
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "service account rate limit exceeded")
		return false
	}
	return true
}

func enforceIdempotencyKey(w http.ResponseWriter, r *http.Request, store IdempotencyStore, resolution authResolution) bool {
	if store == nil || !isMutatingMethod(r.Method) {
		return true
	}
	rawKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if rawKey == "" {
		return true
	}
	actor := "anonymous"
	if resolution.ServiceAccount != nil {
		actor = "service-account:" + resolution.ServiceAccount.ID
	} else if resolution.User != nil && strings.TrimSpace(resolution.User.Subject) != "" {
		actor = "user:" + strings.TrimSpace(resolution.User.Subject)
	}
	scope := strings.Join([]string{actor, r.Method, r.URL.Path, rawKey}, "|")
	reserved, err := store.ReserveIdempotencyKey(r.Context(), scope, configuredIdempotencyKeyTTL())
	if err != nil {
		slog.Warn("idempotency-key reservation failed", "actor", actor, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusServiceUnavailable, "idempotency store unavailable")
		return false
	}
	if !reserved {
		writeError(w, http.StatusConflict, "duplicate idempotency key")
		return false
	}
	return true
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func configuredIdempotencyKeyTTL() time.Duration {
	return firstConfiguredDuration(15*time.Minute, "APEXLOAD_IDEMPOTENCY_KEY_TTL", "APEXLOAD_IDEMPOTENCY_KEY_TTL_SECONDS")
}

func configuredAPIKeyRateLimitPerMinute() int {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_API_KEY_RATE_LIMIT_PER_MINUTE"))
	if raw == "" {
		return 0
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0
	}
	return limit
}

func authorizeAccessRequirement(w http.ResponseWriter, r *http.Request, store storage.Store, resolution authResolution, requirement accessRequirement) bool {
	user := resolution.User
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return false
	}

	if len(requirement.GlobalRoles) > 0 && user.hasGlobalRole(requirement.GlobalRoles...) {
		if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
			writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
			return false
		}
		return true
	}

	if requirement.AuthenticatedOnly {
		if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
			writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
			return false
		}
		return true
	}

	if requirement.ProjectLookup != nil {
		projectID, status, msg := requirement.ProjectLookup(r, store)
		if status != 0 {
			writeError(w, status, msg)
			return false
		}
		if user.hasProjectRole(projectID, requirement.ProjectRoles...) {
			if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
				writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
				return false
			}
			return true
		}
	}

	if requirement.DomainLookup != nil {
		domainID, status, msg := requirement.DomainLookup(r, store)
		if status != 0 {
			writeError(w, status, msg)
			return false
		}
		if user.hasDomainRole(domainID, requirement.DomainRoles...) {
			if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
				writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
				return false
			}
			return true
		}
	}

	if requirement.RepositoryLookup != nil {
		repositoryID, status, msg := requirement.RepositoryLookup(r, store)
		if status != 0 {
			writeError(w, status, msg)
			return false
		}
		if user.hasRepositoryRole(repositoryID, requirement.RepositoryRoles...) {
			if !serviceAccountAllowsRequest(resolution.ServiceAccount, r) {
				writeError(w, http.StatusForbidden, "service account token scope does not allow this operation")
				return false
			}
			return true
		}
	}

	writeError(w, http.StatusForbidden, "forbidden")
	return false
}

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

func accessRequirementForRequest(r *http.Request) (accessRequirement, bool) {
	path := r.URL.Path
	method := r.Method

	if requirement, ok := accessRequirementFromRouteCatalog(method, path); ok {
		return requirement, true
	}

	if path == "/api/v1/system/settings" {
		if method == http.MethodGet {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		if method == http.MethodPut {
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
	}

	if path == "/api/v1/auth/service-accounts" {
		if method == http.MethodGet || method == http.MethodPost {
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
	}

	if path == "/api/v1/auth/users" {
		if method == http.MethodGet || method == http.MethodPost {
			return accessRequirement{AuthenticatedOnly: true}, true
		}
	}

	if path == "/api/v1/auth/user-activity" || path == "/api/v1/auth/user-activity/export" {
		if method == http.MethodGet {
			return accessRequirement{AuthenticatedOnly: true}, true
		}
	}

	if path == "/api/v1/schedules/health" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if path == "/api/v1/run-calendar" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if strings.HasPrefix(path, "/api/v1/auth/service-accounts/") && method == http.MethodPatch {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if strings.HasSuffix(path, "/rotate") && strings.HasPrefix(path, "/api/v1/auth/service-accounts/") && method == http.MethodPost {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if strings.HasPrefix(path, "/api/v1/auth/service-accounts/") && method == http.MethodDelete {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if strings.HasPrefix(path, "/api/v1/auth/users/") {
		switch method {
		case http.MethodGet, http.MethodPatch, http.MethodPost, http.MethodDelete:
			return accessRequirement{AuthenticatedOnly: true}, true
		}
	}

	if path == "/api/v1/auth/audit-events" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if path == "/api/v1/auth/audit-events/export" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if (path == "/api/v1/system/inventory" || path == "/api/v1/system/usage" || path == "/api/v1/system/usage/export" || path == "/api/v1/system/usage/snapshots" || path == "/api/v1/system/parity-status" || path == "/api/v1/system/browser-benchmarks" || path == "/api/v1/desktop/status" || path == "/api/v1/runtime-settings/support-matrix" || path == "/api/v1/protocol-bundles/support-matrix" || path == "/api/v1/protocols/native-support-matrix" || path == "/api/v1/fleet/alerts" || path == "/api/v1/fleet/capacity" || path == "/api/v1/fleet/load-zones") && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if (path == "/api/v1/integrations/apm/profiles" || path == "/api/v1/monitoring/packs" || path == "/api/v1/reports/profiles" || path == "/api/v1/results/imports") && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if (path == "/api/v1/integrations/github/repos" || path == "/api/v1/integrations/github/branches" || path == "/api/v1/integrations/github/contents") && method == http.MethodGet {
		return accessRequirement{AuthenticatedOnly: true}, true
	}

	if path == "/api/v1/library/capabilities" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if path == "/api/v1/library/catalog" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if path == "/api/v1/library/export" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveLibraryTransferProjectID,
		}, true
	}

	if path == "/api/v1/library/import" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveLibraryImportProjectID,
		}, true
	}

	if path == "/api/v1/library/import/archive" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if strings.HasPrefix(path, "/api/v1/library/results/") && strings.HasSuffix(path, "/archive") && method == http.MethodGet {
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveLibraryResultArchiveProjectID,
		}, true
	}

	if path == "/api/v1/library/remotes" {
		if method == http.MethodGet {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		if method == http.MethodPost {
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
	}

	if strings.HasPrefix(path, "/api/v1/library/remotes/") {
		if (method == http.MethodPut || method == http.MethodDelete) && !strings.Contains(path, "/catalog") && !strings.Contains(path, "/pull") && !strings.Contains(path, "/push") && !strings.Contains(path, "/diff") {
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
		if strings.HasSuffix(path, "/catalog") && method == http.MethodGet {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		if strings.HasSuffix(path, "/diff") && method == http.MethodPost {
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveLibraryTransferProjectID,
			}, true
		}
		if strings.HasSuffix(path, "/probe") && method == http.MethodPost {
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
		if (strings.HasSuffix(path, "/pull") || strings.HasSuffix(path, "/push")) && method == http.MethodPost {
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveLibraryTransferProjectID,
			}, true
		}
	}

	if path == "/api/v1/ai/authoring/contract" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if (path == "/api/v1/ai/policy" || path == "/api/v1/ai/review-queue") && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if (path == "/api/v1/ai/drafts" || path == "/api/v1/ai/repairs" || path == "/api/v1/ai/explain") && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveAIAssistanceProjectID,
		}, true
	}

	if path == "/api/v1/results/import" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveExternalResultImportProjectID,
		}, true
	}

	if path == "/api/v1/system/usage/snapshots" && method == http.MethodPost {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if path == "/api/v1/system/generators/drain" && method == http.MethodPatch {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if (path == "/api/v1/archives/retention/plan" || path == "/api/v1/archives/retention/apply") && method == http.MethodPost {
		return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
	}

	if path == "/api/v1/recorder/status" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
				return resolveRecorderListProjectID(r)
			},
		}, true
	}

	if path == "/api/v1/recorder/ca.pem" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
				return resolveRecorderListProjectID(r)
			},
		}, true
	}

	if path == "/api/v1/ws" && method == http.MethodGet {
		if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		}
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveProjectIDFromQuery,
		}, true
	}

	if path == "/api/v1/domains" {
		switch method {
		case http.MethodGet:
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		case http.MethodPost:
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
	}

	if strings.HasPrefix(path, "/api/v1/domains/") {
		if strings.HasSuffix(path, "/application-repository/categories") {
			switch method {
			case http.MethodGet:
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					DomainLookup: resolveDomainIDFromPath,
				}, true
			case http.MethodPost:
				return accessRequirement{
					GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:      []string{RoleEditor, RoleProjectAdmin},
					DomainLookup:     resolveDomainIDFromPath,
					RepositoryRoles:  []string{RoleEditor, RoleProjectAdmin},
					RepositoryLookup: resolveRepositoryIDFromDomainPath,
				}, true
			}
		}
		if strings.HasSuffix(path, "/application-repository") {
			switch method {
			case http.MethodGet:
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					DomainLookup: resolveDomainIDFromPath,
				}, true
			case http.MethodPut:
				return accessRequirement{
					GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
					DomainRoles:      []string{RoleProjectAdmin},
					DomainLookup:     resolveDomainIDFromPath,
					RepositoryRoles:  []string{RoleProjectAdmin},
					RepositoryLookup: resolveRepositoryIDFromDomainPath,
				}, true
			}
		}
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				DomainRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				DomainLookup: resolveDomainIDFromPath,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{GlobalRoles: []string{RoleSystemAdmin}}, true
		}
	}

	if strings.HasPrefix(path, "/api/v1/application-repository/categories/") {
		switch method {
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
				DomainRoles:      []string{RoleProjectAdmin},
				DomainLookup:     resolveCategoryDomainID,
				RepositoryRoles:  []string{RoleProjectAdmin},
				RepositoryLookup: resolveCategoryRepositoryID,
			}, true
		}
	}

	if path == "/api/v1/projects" {
		switch method {
		case http.MethodGet:
			return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:      []string{RoleProjectAdmin, RoleSystemAdmin},
				DomainRoles:      []string{RoleEditor, RoleProjectAdmin},
				DomainLookup:     resolveProjectCreateDomainID,
				RepositoryRoles:  []string{RoleEditor, RoleProjectAdmin},
				RepositoryLookup: resolveProjectCreateRepositoryID,
			}, true
		}
	}

	if strings.HasPrefix(path, "/api/v1/projects/") {
		switch method {
		case http.MethodGet:
			if strings.HasSuffix(path, "/releases") {
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("id")), 0, ""
					},
				}, true
			}
			if strings.HasSuffix(path, "/folders") {
				return accessRequirement{
					GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("pid")), 0, ""
					},
				}, true
			}
			return accessRequirement{
				GlobalRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
					return normalizeProjectID(r.PathValue("id")), 0, ""
				},
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:  []string{RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles: []string{RoleProjectAdmin},
				ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
					return normalizeProjectID(r.PathValue("id")), 0, ""
				},
			}, true
		case http.MethodPost:
			if strings.HasSuffix(path, "/releases") {
				return accessRequirement{
					GlobalRoles:      []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:     []string{RoleEditor, RoleProjectAdmin},
					DomainRoles:      []string{RoleEditor, RoleProjectAdmin},
					RepositoryRoles:  []string{RoleEditor, RoleProjectAdmin},
					DomainLookup:     resolveProjectDomainIDFromPath,
					RepositoryLookup: resolveProjectRepositoryIDFromPath,
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("id")), 0, ""
					},
				}, true
			}
			if strings.HasSuffix(path, "/folders") {
				return accessRequirement{
					GlobalRoles:  []string{RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles: []string{RoleProjectAdmin},
					ProjectLookup: func(r *http.Request, _ storage.Store) (string, int, string) {
						return normalizeProjectID(r.PathValue("pid")), 0, ""
					},
				}, true
			}
		}
	}

	if strings.HasPrefix(path, "/api/v1/folders/") {
		switch method {
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleProjectAdmin},
				ProjectLookup: resolveFolderProjectID,
			}, true
		}
	}

	if path == "/api/v1/applications" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/applications/") {
		if strings.HasSuffix(path, "/releases") {
			switch method {
			case http.MethodGet:
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveApplicationProjectID,
				}, true
			case http.MethodPost:
				return accessRequirement{
					GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveApplicationProjectID,
				}, true
			}
		}

		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationProjectID,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveApplicationProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/releases/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveReleaseProjectID,
			}, true
		case http.MethodPut, http.MethodPost, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveReleaseProjectID,
			}, true
		}
	}

	if path == "/api/v1/scripts" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" && !r.URL.Query().Has("folder_id") {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptCreateProjectID,
			}, true
		}
	}

	if path == "/api/v1/scripts/replay" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveDraftScriptProjectID,
		}, true
	}

	if path == "/api/v1/scripts/debug/sessions" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveDraftScriptProjectID,
		}, true
	}

	if path == "/api/v1/scripts/browser-replay/sessions" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveDraftScriptProjectID,
		}, true
	}

	if path == "/api/v1/scripts/import/jmeter/save" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveJMeterSaveProjectID,
		}, true
	}

	if path == "/api/v1/scripts/import/jmeter/support-matrix" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if path == "/api/v1/scripts/import/loadrunner/support-matrix" && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if (path == "/api/v1/scripts/web-api/support-matrix" || path == "/api/v1/scripts/import/devweb/support-matrix" || path == "/api/v1/scripts/import/k6/support-matrix" || path == "/api/v1/scripts/import/oracle-ebusiness/support-matrix") && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if (path == "/api/v1/scripts/authoring/capabilities" || path == "/api/v1/scripts/authoring/templates" || path == "/api/v1/scripts/authoring/components") && method == http.MethodGet {
		return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
	}

	if path == "/api/v1/scripts/import/loadrunner" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/loadrunner/validate" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/jmeter" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/jmeter/validate" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromForm,
		}, true
	}

	if path == "/api/v1/scripts/import/github" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromJSON,
		}, true
	}

	if strings.HasPrefix(path, "/api/v1/scripts/") && strings.HasSuffix(path, "/github/push") && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveScriptProjectID,
		}, true
	}

	if strings.HasPrefix(path, "/api/v1/scripts/") && strings.HasSuffix(path, "/github/status") && method == http.MethodGet {
		return accessRequirement{
			GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveScriptProjectID,
		}, true
	}

	if path == "/api/v1/scripts/import/sample" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromJSON,
		}, true
	}

	if path == "/api/v1/scripts/import/oracle-ebusiness/review" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromJSONOrForm,
		}, true
	}

	if (path == "/api/v1/scripts/import/devweb" || path == "/api/v1/scripts/import/har" || path == "/api/v1/scripts/import/k6" || path == "/api/v1/scripts/import/openapi" || path == "/api/v1/scripts/import/postman" || path == "/api/v1/scripts/import/wsdl") && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveImportProjectIDFromJSONOrForm,
		}, true
	}

	if path == "/api/v1/scripts/export/k6" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveK6ExportProjectID,
		}, true
	}

	if path == "/api/v1/data-pools" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/data-pools/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolProjectID,
			}, true
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveDataPoolProjectID,
			}, true
		}
	}

	if path == "/api/v1/tests" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("application_id")) != "" || strings.TrimSpace(r.URL.Query().Get("release_id")) != "" || strings.TrimSpace(r.URL.Query().Get("scenario_id")) != "" {
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveTestContextProjectID,
				}, true
			}
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" && !r.URL.Query().Has("folder_id") {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestCreateProjectID,
			}, true
		}
	}

	if path == "/api/v1/scenarios" {
		switch method {
		case http.MethodGet:
			if strings.TrimSpace(r.URL.Query().Get("project_id")) == "" {
				return accessRequirement{GlobalRoles: []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin}}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioListProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioCreateProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.Contains(path, "/schedules/") && strings.Contains(path, "/approvals/") && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleProjectAdmin},
			ProjectLookup: resolveScenarioProjectID,
		}, true
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/scenarios/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		case http.MethodPut, http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScenarioProjectID,
			}, true
		}
	}

	if path == "/api/v1/tests/estimate" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveTestCreateProjectID,
		}, true
	}

	if path == "/api/v1/tests/preflight" && method == http.MethodPost {
		return accessRequirement{
			GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
			ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
			ProjectLookup: resolveTestCreateProjectID,
		}, true
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/tests/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		case http.MethodDelete:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		case http.MethodPatch:
			lookup := resolveTestProjectID
			if strings.HasSuffix(path, "/move") {
				lookup = resolveTestMoveProjectID
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: lookup,
			}, true
		case http.MethodPost:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveTestProjectID,
			}, true
		}
	}

	if r.PathValue("id") != "" && strings.HasPrefix(path, "/api/v1/scripts/") {
		switch method {
		case http.MethodGet:
			return accessRequirement{
				GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		case http.MethodPut, http.MethodDelete, http.MethodPatch:
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		case http.MethodPost:
			if strings.HasSuffix(path, "/validate") || strings.HasSuffix(path, "/export") {
				return accessRequirement{
					GlobalRoles:   []string{RoleViewer, RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
					ProjectRoles:  []string{RoleViewer, RoleEditor, RoleProjectAdmin},
					ProjectLookup: resolveScriptProjectID,
				}, true
			}
			return accessRequirement{
				GlobalRoles:   []string{RoleEditor, RoleProjectAdmin, RoleSystemAdmin},
				ProjectRoles:  []string{RoleEditor, RoleProjectAdmin},
				ProjectLookup: resolveScriptProjectID,
			}, true
		}
	}

	return accessRequirement{}, false
}

func resolveFolderProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "folder storage is not configured"
	}
	folder, err := store.GetFolder(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "folder not found"
	}
	return normalizeProjectID(folder.ProjectID), 0, ""
}

func resolveProjectIDFromQuery(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveDomainIDFromPath(r *http.Request, _ storage.Store) (string, int, string) {
	return normalizeDomainID(r.PathValue("id")), 0, ""
}

func resolveRepositoryIDFromDomainPath(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository storage is not configured"
	}
	repository, err := store.GetApplicationRepository(normalizeDomainID(r.PathValue("id")))
	if err != nil {
		return "", http.StatusNotFound, "application repository not found"
	}
	return strings.TrimSpace(repository.ID), 0, ""
}

func resolveCategoryDomainID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository category storage is not configured"
	}
	category, err := store.GetRepositoryCategory(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "category not found"
	}
	return normalizeDomainID(category.DomainID), 0, ""
}

func resolveCategoryRepositoryID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository category storage is not configured"
	}
	category, err := store.GetRepositoryCategory(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "category not found"
	}
	return strings.TrimSpace(category.RepositoryID), 0, ""
}

func resolveProjectCreateDomainID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		DomainID   string `json:"domain_id"`
		CategoryID string `json:"category_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil && err != io.EOF {
		return "", http.StatusBadRequest, "invalid request body"
	}
	if strings.TrimSpace(req.DomainID) != "" {
		return normalizeDomainID(req.DomainID), 0, ""
	}
	if strings.TrimSpace(req.CategoryID) != "" && store != nil {
		category, err := store.GetRepositoryCategory(req.CategoryID)
		if err != nil {
			return "", http.StatusNotFound, "category not found"
		}
		return normalizeDomainID(category.DomainID), 0, ""
	}
	return storage.DefaultDomainID, 0, ""
}

func resolveProjectCreateRepositoryID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "repository storage is not configured"
	}
	var req struct {
		DomainID   string `json:"domain_id"`
		CategoryID string `json:"category_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil && err != io.EOF {
		return "", http.StatusBadRequest, "invalid request body"
	}
	if strings.TrimSpace(req.CategoryID) != "" {
		category, err := store.GetRepositoryCategory(req.CategoryID)
		if err != nil {
			return "", http.StatusNotFound, "category not found"
		}
		return strings.TrimSpace(category.RepositoryID), 0, ""
	}
	repository, err := store.GetApplicationRepository(normalizeDomainID(req.DomainID))
	if err != nil {
		return "", http.StatusNotFound, "application repository not found"
	}
	return strings.TrimSpace(repository.ID), 0, ""
}

func resolveProjectDomainIDFromPath(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "project storage is not configured"
	}
	project, err := store.GetProject(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "project not found"
	}
	return normalizeDomainID(project.DomainID), 0, ""
}

func resolveProjectRepositoryIDFromPath(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "project storage is not configured"
	}
	project, err := store.GetProject(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "project not found"
	}
	if strings.TrimSpace(project.CategoryID) != "" {
		category, err := store.GetRepositoryCategory(project.CategoryID)
		if err == nil && category != nil {
			return strings.TrimSpace(category.RepositoryID), 0, ""
		}
	}
	repository, err := store.GetApplicationRepository(normalizeDomainID(project.DomainID))
	if err != nil {
		return "", http.StatusNotFound, "application repository not found"
	}
	return strings.TrimSpace(repository.ID), 0, ""
}

func resolveApplicationListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveApplicationCreateProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveApplicationProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "application storage is not configured"
	}
	application, err := store.GetApplication(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "application not found"
	}
	return normalizeProjectID(application.ProjectID), 0, ""
}

func resolveReleaseProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "release storage is not configured"
	}
	release, err := store.GetRelease(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "release not found"
	}
	if strings.TrimSpace(release.ProjectID) != "" {
		return normalizeProjectID(release.ProjectID), 0, ""
	}
	application, err := store.GetApplication(release.ApplicationID)
	if err != nil {
		return "", http.StatusNotFound, "application not found"
	}
	return normalizeProjectID(application.ProjectID), 0, ""
}

func resolveScriptProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "script storage is not configured"
	}
	script, err := store.GetScript(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "script not found"
	}
	return normalizeProjectID(script.ProjectID), 0, ""
}

func resolveScriptListProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if !r.URL.Query().Has("folder_id") {
		return "", http.StatusBadRequest, "project_id or folder_id is required for project-scoped script access"
	}
	return resolveFolderProjectIDValue(r.URL.Query().Get("folder_id"), store)
}

func resolveTestProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "test storage is not configured"
	}
	test, err := store.GetTest(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "test not found"
	}
	return normalizeProjectID(test.ProjectID), 0, ""
}

func resolveTestListProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if !r.URL.Query().Has("folder_id") {
		return "", http.StatusBadRequest, "project_id or folder_id is required for project-scoped test access"
	}
	return resolveFolderProjectIDValue(r.URL.Query().Get("folder_id"), store)
}

func resolveTestContextProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "test storage is not configured"
	}
	resolvedProjectID := ""
	mergeProjectID := func(candidate string) (int, string) {
		normalized := normalizeProjectID(candidate)
		if resolvedProjectID == "" {
			resolvedProjectID = normalized
			return 0, ""
		}
		if resolvedProjectID != normalized {
			return http.StatusBadRequest, "test context filters must belong to the same project"
		}
		return 0, ""
	}
	if applicationID := strings.TrimSpace(r.URL.Query().Get("application_id")); applicationID != "" {
		application, err := store.GetApplication(applicationID)
		if err != nil {
			return "", http.StatusNotFound, "application not found"
		}
		if status, msg := mergeProjectID(application.ProjectID); status != 0 {
			return "", status, msg
		}
	}
	if releaseID := strings.TrimSpace(r.URL.Query().Get("release_id")); releaseID != "" {
		releaseReq := r.Clone(r.Context())
		releaseReq.SetPathValue("id", releaseID)
		releaseProjectID, status, msg := resolveReleaseProjectID(releaseReq, store)
		if status != 0 {
			return "", status, msg
		}
		if status, msg := mergeProjectID(releaseProjectID); status != 0 {
			return "", status, msg
		}
	}
	if scenarioID := strings.TrimSpace(r.URL.Query().Get("scenario_id")); scenarioID != "" {
		scenarioReq := r.Clone(r.Context())
		scenarioReq.SetPathValue("id", scenarioID)
		scenarioProjectID, status, msg := resolveScenarioProjectID(scenarioReq, store)
		if status != 0 {
			return "", status, msg
		}
		if status, msg := mergeProjectID(scenarioProjectID); status != 0 {
			return "", status, msg
		}
	}
	if resolvedProjectID == "" {
		return "", http.StatusBadRequest, "application_id, release_id, or scenario_id is required"
	}
	return resolvedProjectID, 0, ""
}

func resolveTestCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req CreateTestRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}

	requestedProjectID := normalizeProjectID(req.ProjectID)
	folderProjectID := ""
	if req.FolderID != nil {
		projectID, status, msg := resolveFolderProjectIDValue(*req.FolderID, store)
		if status != 0 {
			return "", status, msg
		}
		folderProjectID = projectID
		if req.ProjectID != "" && requestedProjectID != folderProjectID {
			return "", http.StatusBadRequest, "project_id does not match folder project"
		}
	}

	inferredProjectID, status, msg := resolveCreateTestScriptProjectID(req, store)
	if status != 0 {
		return "", status, msg
	}
	if inferredProjectID != "" {
		if req.ProjectID != "" && requestedProjectID != inferredProjectID {
			return "", http.StatusBadRequest, "project_id does not match test scripts"
		}
		if folderProjectID != "" && folderProjectID != inferredProjectID {
			return "", http.StatusBadRequest, "folder project does not match test scripts"
		}
	}

	if folderProjectID != "" {
		return folderProjectID, 0, ""
	}
	if req.ProjectID != "" {
		return requestedProjectID, 0, ""
	}
	if inferredProjectID != "" {
		return inferredProjectID, 0, ""
	}
	return normalizeProjectID(""), 0, ""
}

func resolveCreateTestScriptProjectID(req CreateTestRequest, store storage.Store) (string, int, string) {
	scriptIDs := make([]string, 0, len(req.Groups)+1)
	if req.ScriptID != "" {
		scriptIDs = append(scriptIDs, req.ScriptID)
	}
	for _, group := range req.Groups {
		if group.ScriptID != "" {
			scriptIDs = append(scriptIDs, group.ScriptID)
		}
	}
	projectID := ""
	for _, scriptID := range scriptIDs {
		script, err := store.GetScript(scriptID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "test scripts span multiple projects"
		}
	}
	return projectID, 0, ""
}

func resolveTestMoveProjectID(r *http.Request, store storage.Store) (string, int, string) {
	projectID, status, msg := resolveTestProjectID(r, store)
	if status != 0 {
		return "", status, msg
	}
	var req struct {
		FolderID *string `json:"folder_id"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.FolderID == nil {
		return projectID, 0, ""
	}
	destinationProjectID, destStatus, destMsg := resolveFolderProjectIDValue(*req.FolderID, store)
	if destStatus != 0 {
		return "", destStatus, destMsg
	}
	if destinationProjectID != projectID {
		return "", http.StatusBadRequest, "destination folder belongs to a different project"
	}
	return projectID, 0, ""
}

func resolveScenarioProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "scenario storage is not configured"
	}
	_, scripts, err := store.GetScenario(r.PathValue("id"))
	if err != nil {
		return "", http.StatusNotFound, "scenario not found"
	}
	return resolveProjectIDFromScenarioScripts(scripts, store)
}

func resolveScenarioListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped scenario access"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveScenarioCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req CreateScenarioRequest
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	scripts := make([]storage.ScenarioScript, 0, len(req.Groups))
	for index, group := range req.Groups {
		scripts = append(scripts, storage.ScenarioScript{Order: index, ScriptID: group.ScriptID})
	}
	return resolveProjectIDFromScenarioScripts(scripts, store)
}

func resolveProjectIDFromScenarioScripts(scripts []storage.ScenarioScript, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "script storage is not configured"
	}
	projectID := ""
	for _, scenarioScript := range scripts {
		if strings.TrimSpace(scenarioScript.ScriptID) == "" {
			continue
		}
		script, err := store.GetScript(scenarioScript.ScriptID)
		if err != nil {
			return "", http.StatusNotFound, "script not found"
		}
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "scenario scripts span multiple projects"
		}
	}
	if projectID == "" {
		return normalizeProjectID(""), 0, ""
	}
	return projectID, 0, ""
}

func resolveDraftScriptProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		Script *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.Script == nil {
		return "", http.StatusBadRequest, "script is required"
	}
	return normalizeProjectID(req.Script.ProjectID), 0, ""
}

func resolveJMeterSaveProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		Scripts []struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"scripts"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if len(req.Scripts) == 0 {
		return "", http.StatusBadRequest, "at least one script is required"
	}
	projectID := ""
	for _, script := range req.Scripts {
		candidate := normalizeProjectID(script.ProjectID)
		if projectID == "" {
			projectID = candidate
			continue
		}
		if candidate != projectID {
			return "", http.StatusBadRequest, "imported scripts span multiple projects"
		}
	}
	return projectID, 0, ""
}

func resolveDataPoolListProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped data pool access"
	}
	return normalizeProjectID(projectID), 0, ""
}

func resolveDataPoolCreateProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped data pool access"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveDataPoolProjectID(r *http.Request, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "storage is not configured"
	}
	pool, err := store.GetDataPool(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", http.StatusNotFound, "data pool not found"
		}
		return "", http.StatusInternalServerError, "failed to load data pool"
	}
	return normalizeProjectID(pool.ProjectID), 0, ""
}

func resolveRecorderListProjectID(r *http.Request) (string, int, string) {
	projectID := normalizeProjectID(r.URL.Query().Get("project_id"))
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped recorder access"
	}
	return projectID, 0, ""
}

func resolveRecorderCreateProjectID(r *http.Request) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	projectID := normalizeProjectID(req.ProjectID)
	if projectID == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped recorder access"
	}
	return projectID, 0, ""
}

func resolveRecorderSessionProjectID(rec *recorder.Recorder) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if rec == nil {
			return "", http.StatusServiceUnavailable, "recorder is not configured"
		}
		session, ok := rec.GetSession(r.PathValue("id"))
		if !ok {
			return "", http.StatusNotFound, "recording session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveBrowserReplaySessionProjectID(manager *BrowserReplayManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "browser replay is not configured"
		}
		session, ok := manager.Get(r.PathValue("session_id"))
		if !ok {
			return "", http.StatusNotFound, "browser replay session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveScriptDebugSessionProjectID(manager *ScriptDebugManager) requestProjectLookup {
	return func(r *http.Request) (string, int, string) {
		if manager == nil {
			return "", http.StatusServiceUnavailable, "script debugger is not configured"
		}
		session, ok := manager.Get(r.PathValue("session_id"))
		if !ok {
			return "", http.StatusNotFound, "script debug session not found"
		}
		return normalizeProjectID(session.ProjectID), 0, ""
	}
}

func resolveImportProjectIDFromForm(r *http.Request, _ storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	if err := r.ParseMultipartForm(50 << 20); err == nil {
		if projectID := strings.TrimSpace(r.FormValue("project_id")); projectID != "" {
			return normalizeProjectID(projectID), 0, ""
		}
	}
	return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
}

func resolveImportProjectIDFromJSON(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveImportProjectIDFromJSONOrForm(r *http.Request, store storage.Store) (string, int, string) {
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		return normalizeProjectID(projectID), 0, ""
	}
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return resolveImportProjectIDFromForm(r, store)
	}
	if strings.HasPrefix(contentType, "application/json") {
		return resolveImportProjectIDFromJSON(r, store)
	}
	return "", http.StatusBadRequest, "project_id is required for project-scoped import access"
}

func resolveK6ExportProjectID(r *http.Request, _ storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
		Script    *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if projectID := normalizeProjectID(req.ProjectID); projectID != "" {
		return projectID, 0, ""
	}
	if req.Script != nil {
		if projectID := normalizeProjectID(req.Script.ProjectID); projectID != "" {
			return projectID, 0, ""
		}
	}
	return "", http.StatusBadRequest, "project_id or script.project_id is required for project-scoped export access"
}

func resolveAIAssistanceProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID        string `json:"project_id,omitempty"`
		ExistingScriptID string `json:"existing_script_id,omitempty"`
		ScriptID         string `json:"script_id,omitempty"`
		TargetType       string `json:"target_type,omitempty"`
		TargetID         string `json:"target_id,omitempty"`
		Script           *struct {
			ProjectID string `json:"project_id,omitempty"`
		} `json:"script,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if projectID := normalizeProjectID(req.ProjectID); projectID != "" {
		return projectID, 0, ""
	}
	if req.Script != nil {
		return normalizeProjectID(req.Script.ProjectID), 0, ""
	}
	scriptID := firstNonEmpty(req.ScriptID, req.ExistingScriptID)
	if scriptID == "" && strings.EqualFold(strings.TrimSpace(req.TargetType), "script") {
		scriptID = strings.TrimSpace(req.TargetID)
	}
	if scriptID != "" && store != nil {
		script, err := store.GetScript(scriptID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", http.StatusNotFound, "script not found"
			}
			return "", http.StatusInternalServerError, "failed to load script"
		}
		return normalizeProjectID(script.ProjectID), 0, ""
	}
	return "", 0, ""
}

func resolveExternalResultImportProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID string `json:"project_id,omitempty"`
		ReleaseID string `json:"release_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if strings.TrimSpace(req.ProjectID) != "" {
		return normalizeProjectID(req.ProjectID), 0, ""
	}
	if releaseID := strings.TrimSpace(req.ReleaseID); releaseID != "" {
		if store == nil {
			return "", http.StatusServiceUnavailable, "release storage is not configured"
		}
		releaseReq := r.Clone(r.Context())
		releaseReq.SetPathValue("id", releaseID)
		return resolveReleaseProjectID(releaseReq, store)
	}
	return normalizeProjectID(""), 0, ""
}

func resolveScriptCreateProjectID(r *http.Request, store storage.Store) (string, int, string) {
	var req struct {
		ProjectID string  `json:"project_id,omitempty"`
		FolderID  *string `json:"folder_id,omitempty"`
	}
	if err := decodeRequestBody(r, &req); err != nil {
		return "", http.StatusBadRequest, "invalid request body: " + err.Error()
	}
	if req.FolderID != nil {
		return resolveFolderProjectIDValue(*req.FolderID, store)
	}
	return normalizeProjectID(req.ProjectID), 0, ""
}

func resolveFolderProjectIDValue(folderID string, store storage.Store) (string, int, string) {
	if store == nil {
		return "", http.StatusServiceUnavailable, "folder storage is not configured"
	}
	folder, err := store.GetFolder(folderID)
	if err != nil {
		return "", http.StatusNotFound, "folder not found"
	}
	return normalizeProjectID(folder.ProjectID), 0, ""
}

func decodeRequestBody(r *http.Request, target any) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return io.EOF
	}
	return json.Unmarshal(body, target)
}

func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))

			// Only ever reflect an Origin we have explicitly validated. When no
			// allow-list is configured this permits same-origin requests only —
			// never an arbitrary reflected origin.
			if origin != "" && originAllowed(allowedOrigins, r) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-APEXLoad-API-Key, X-APEXLoad-Dev-View, X-APEXLoad-Dev-Project-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// defaultMaxRequestBodyBytes is a global backstop for request body size. It is
// set above the largest explicit per-handler http.MaxBytesReader limit (100 MiB
// for LoadRunner imports) so handlers with their own, smaller limit always win,
// while handlers that decode the body without any limit are still bounded.
const defaultMaxRequestBodyBytes = 128 << 20

// bodyLimitMiddleware caps request body size as a defense against memory-
// exhaustion DoS on handlers that decode the body without their own limit.
func bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Limit only methods that carry a body; leave GET/HEAD (incl. the
		// WebSocket upgrade, which hijacks the connection) untouched.
		if r.Body != nil && isMutatingMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, defaultMaxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware applies baseline security response headers to every
// response (API, dashboard, and WebSocket handshakes). HSTS is only emitted on
// requests that arrived over TLS (directly or via a TLS-terminating proxy).
func securityHeadersMiddleware(next http.Handler) http.Handler {
	// The bundled dashboard loads the Inter webfont from Google Fonts; allow
	// those specific origins for stylesheet and font fetches.
	const csp = "default-src 'self'; " +
		"script-src 'self'; " +
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
		"img-src 'self' data: blob:; " +
		"font-src 'self' data: https://fonts.gstatic.com; " +
		"connect-src 'self' ws: wss:; " +
		"worker-src 'self' blob:; " +
		"object-src 'none'; " +
		"base-uri 'self'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", csp)
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rw, r)

		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wrote {
		rw.status = code
		rw.wrote = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wrote {
		rw.wrote = true
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("upstream ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
