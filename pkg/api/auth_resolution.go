package api

// auth_resolution.go — split from middleware.go (same package, behavior-preserving).
// Request authentication: identity/context resolution, local bindings, API-key and
// service-account auth, role-header parsing, and AuthenticatedUser role-check methods.

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const userIdentitySeenWriteInterval = time.Minute

var userIdentitySeenThrottle = struct {
	sync.Mutex
	lastWriteByUserID map[string]time.Time
}{
	lastWriteByUserID: make(map[string]time.Time),
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
	// Trusted-headers mode: enforce the trusted-proxy gate before honoring any
	// identity headers, so a client that reaches the controller directly cannot forge
	// identity. When the gate is unconfigured we allow in dev but fail closed in
	// production (startup also refuses to boot — see enforceAuthPosture).
	if err := enforceTrustedProxyGate(r, settings); err != nil {
		return authResolution{}, err
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

// trustedProxySecretHeader is the header a fronting gateway sets to a shared secret
// to prove the request transited the trusted ingress (alternative to a CIDR allowlist).
const trustedProxySecretHeader = "X-APEXLoad-Proxy-Secret"

// trustedProxyGateConfigured reports whether any trust gate (CIDR allowlist or shared
// secret) is configured for trusted_headers mode.
func trustedProxyGateConfigured(settings AuthSettings) bool {
	if strings.TrimSpace(settings.TrustedProxySecretEnvVar) != "" {
		return true
	}
	for _, cidr := range settings.TrustedProxyCIDRs {
		if strings.TrimSpace(cidr) != "" {
			return true
		}
	}
	return false
}

// trustedProxySharedSecret resolves and validates the secret used to authenticate
// forwarded identity headers. In production this secret protects an administrative
// trust boundary, so accepting a short, guessable value would let a direct client
// forge the complete upstream identity and role-header set.
func trustedProxySharedSecret(settings AuthSettings) (string, error) {
	env := strings.TrimSpace(settings.TrustedProxySecretEnvVar)
	if env == "" {
		return "", fmt.Errorf("trusted-proxy shared-secret environment variable is not configured")
	}
	secret := strings.TrimSpace(os.Getenv(env))
	if secret == "" {
		return "", fmt.Errorf("trusted-proxy shared-secret environment variable %q is empty", env)
	}
	if productionMode() && len([]byte(secret)) < productionSecretMinBytes {
		return "", fmt.Errorf("trusted-proxy shared-secret environment variable %q must contain at least %d bytes in production", env, productionSecretMinBytes)
	}
	return secret, nil
}

// enforceTrustedProxyGate denies a trusted-headers request that did not arrive through
// the trusted ingress. When no gate is configured it allows in non-production (legacy
// behavior) but denies in production as a defense-in-depth backstop.
func enforceTrustedProxyGate(r *http.Request, settings AuthSettings) error {
	if !trustedProxyGateConfigured(settings) {
		if productionMode() {
			return fmt.Errorf("trusted-headers auth requires a trusted-proxy allowlist or shared secret in production")
		}
		return nil
	}
	if trustedProxyRequestAllowed(r, settings) {
		return nil
	}
	return fmt.Errorf("request did not arrive from a trusted proxy")
}

func trustedProxyRequestAllowed(r *http.Request, settings AuthSettings) bool {
	if env := strings.TrimSpace(settings.TrustedProxySecretEnvVar); env != "" {
		if expected, err := trustedProxySharedSecret(settings); err == nil {
			provided := strings.TrimSpace(r.Header.Get(trustedProxySecretHeader))
			if provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1 {
				return true
			}
		}
	}
	if ip := requestSourceIP(r); ip != nil {
		for _, raw := range settings.TrustedProxyCIDRs {
			cidr := strings.TrimSpace(raw)
			if cidr == "" {
				continue
			}
			if _, network, err := net.ParseCIDR(cidr); err == nil && network.Contains(ip) {
				return true
			}
			// Allow a bare IP (no mask) as a convenience.
			if bare := net.ParseIP(cidr); bare != nil && bare.Equal(ip) {
				return true
			}
		}
	}
	return false
}

// requestSourceIP returns the immediate peer IP (r.RemoteAddr). Behind a
// TLS-terminating proxy this is the proxy's address — exactly what the allowlist
// should match. X-Forwarded-For is intentionally NOT trusted here (it is client-
// controlled and would defeat the gate).
func requestSourceIP(r *http.Request) net.IP {
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return nil
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(strings.TrimSpace(host))
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
				return bootstrapFirstAdmin(upstream, store)
			}
			return nil, fmt.Errorf("user is not onboarded")
		}
		return nil, fmt.Errorf("user access lookup failed")
	}
	if local == nil || !local.Active {
		return nil, fmt.Errorf("user is disabled")
	}
	if upstream.SessionScoped {
		boundUserID := strings.TrimSpace(upstream.ID)
		if boundUserID == "" || boundUserID != strings.TrimSpace(local.ID) {
			return nil, fmt.Errorf("session revoked: local identity changed")
		}
	}
	// Session revocation: an OIDC cookie minted before the user's epoch was bumped is
	// rejected. Only applies to session-scoped identities (OIDC); trusted-header
	// identities carry no epoch and are unaffected.
	if upstream.SessionScoped && upstream.SessionEpoch < local.SessionEpoch {
		return nil, fmt.Errorf("session revoked")
	}
	now := time.Now().UTC()
	previousDisplayName := local.DisplayName
	previousEmail := local.Email
	local.LastSeenAt = &now
	local.DisplayName = firstNonEmpty(upstream.DisplayName, local.DisplayName)
	local.Email = firstNonEmpty(upstream.Email, local.Email)
	if shouldUpdateUserIdentitySeen(local.ID, previousDisplayName, previousEmail, local.DisplayName, local.Email, now) {
		local.UpdatedAt = now
		if err := store.UpdateUserIdentitySeen(local.ID, local.DisplayName, local.Email, now, now); err != nil {
			slog.Warn("failed to persist user identity last-seen update", "user_id", local.ID, "error", err)
		}
	}
	resolved := authenticatedUserFromStoredUser(local)
	resolved.SessionScoped = upstream.SessionScoped
	resolved.SessionEpoch = upstream.SessionEpoch
	resolved.SessionExpiresAt = upstream.SessionExpiresAt
	return resolved, nil
}

func shouldUpdateUserIdentitySeen(userID string, previousDisplayName string, previousEmail string, nextDisplayName string, nextEmail string, now time.Time) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return true
	}
	profileChanged := previousDisplayName != nextDisplayName || previousEmail != nextEmail
	userIdentitySeenThrottle.Lock()
	defer userIdentitySeenThrottle.Unlock()
	if profileChanged {
		userIdentitySeenThrottle.lastWriteByUserID[userID] = now
		return true
	}
	lastWrite, ok := userIdentitySeenThrottle.lastWriteByUserID[userID]
	if !ok || now.Sub(lastWrite) >= userIdentitySeenWriteInterval {
		userIdentitySeenThrottle.lastWriteByUserID[userID] = now
		return true
	}
	return false
}

func resetUserIdentitySeenThrottleForTests() {
	userIdentitySeenThrottle.Lock()
	defer userIdentitySeenThrottle.Unlock()
	userIdentitySeenThrottle.lastWriteByUserID = make(map[string]time.Time)
}

// authenticatedUserFromStoredUser folds a stored user's local role bindings into an
// AuthenticatedUser (global/project/domain/application_repository role sets). It is the
// shared resolution used both by the per-request auth path (applyLocalUserBindings, which
// pre-merges DisplayName/Email from the upstream identity onto local before calling this)
// and by the admin effective-access simulator, so both compute access identically.
func authenticatedUserFromStoredUser(local *storage.User) *AuthenticatedUser {
	user := &AuthenticatedUser{
		ID:              local.ID,
		Subject:         local.Subject,
		DisplayName:     local.DisplayName,
		Email:           local.Email,
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
	return user
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

// bootstrapFirstAdmin handles the empty-user-store path (no local users exist yet).
//
// Non-production: preserve the historical behavior of honoring the upstream-asserted
// identity/roles, so local first-run and the existing auth test suite keep working.
// (Pure dev mode bypasses auth entirely; this path is only reached in trusted_headers
// or OIDC mode, i.e. a local/staging gateway setup.)
//
// Production: the empty-store window is an attack surface — if the controller is
// reachable directly and the gateway is not perfectly stripping client headers, an
// unauthenticated client could assert system_admin. So it is OFF by default and must be
// explicitly enabled (APEXLOAD_ALLOW_BOOTSTRAP_ADMIN); when enabled it grants a fixed
// system_admin (NOT the asserted roles), may be pinned to a single subject
// (APEXLOAD_BOOTSTRAP_SUBJECT), and persists + audits the grant so the window closes
// after the first request and the event is recorded exactly once.
type bootstrapFirstAdminStore interface {
	BootstrapFirstAdmin(user *storage.User, binding *storage.UserRoleBinding, event *storage.AuditEvent) (bool, error)
}

func bootstrapFirstAdmin(upstream *AuthenticatedUser, store storage.Store) (*AuthenticatedUser, error) {
	if !productionMode() {
		return upstream, nil
	}
	if !bootstrapAdminEnabled() {
		return nil, fmt.Errorf("user is not onboarded")
	}
	subject := strings.TrimSpace(upstream.Subject)
	if subject == "" {
		return nil, fmt.Errorf("user is not onboarded")
	}
	if pinned := strings.TrimSpace(os.Getenv("APEXLOAD_BOOTSTRAP_SUBJECT")); pinned != "" && pinned != subject {
		return nil, fmt.Errorf("user is not onboarded")
	}
	now := time.Now().UTC()
	user := &storage.User{
		ID: uuid.New().String(), Subject: subject, Email: upstream.Email, DisplayName: upstream.DisplayName,
		Active: true, CreatedAt: now, UpdatedAt: now, LastSeenAt: &now,
	}
	binding := &storage.UserRoleBinding{
		ID: uuid.New().String(), UserID: user.ID, ScopeType: "global", Role: RoleSystemAdmin, CreatedAt: now, UpdatedAt: now,
	}
	event := &storage.AuditEvent{
		ID: uuid.New().String(), Action: "auth.bootstrap.admin_granted", ActorType: "system",
		ActorSubject: subject, ActorDisplayName: upstream.DisplayName, ResourceType: "user", ResourceID: user.ID,
		Status: "success", Details: map[string]any{"role": RoleSystemAdmin, "reason": "empty user store bootstrap"}, CreatedAt: now,
	}
	if bootstrapStore, ok := store.(bootstrapFirstAdminStore); ok {
		granted, err := bootstrapStore.BootstrapFirstAdmin(user, binding, event)
		if err != nil {
			return nil, fmt.Errorf("bootstrap administrator provisioning failed")
		}
		if !granted {
			return nil, fmt.Errorf("user is not onboarded")
		}
	} else {
		if err := store.SaveUser(user); err != nil {
			return nil, fmt.Errorf("bootstrap administrator provisioning failed")
		}
		if err := store.SaveUserRoleBinding(binding); err != nil {
			return nil, fmt.Errorf("bootstrap administrator provisioning failed")
		}
		if err := store.SaveAuditEvent(event); err != nil {
			slog.Warn("failed to persist bootstrap administrator audit event", "user_id", user.ID, "error", err)
		}
	}
	slog.Warn("granted bootstrap system administrator on empty user store", "subject", subject)
	return &AuthenticatedUser{
		ID: user.ID, Subject: subject, DisplayName: upstream.DisplayName, Email: upstream.Email,
		GlobalRoles:     map[string]struct{}{RoleSystemAdmin: {}},
		ProjectRoles:    map[string]map[string]struct{}{},
		DomainRoles:     map[string]map[string]struct{}{},
		RepositoryRoles: map[string]map[string]struct{}{},
	}, nil
}

func bootstrapAdminEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ALLOW_BOOTSTRAP_ADMIN"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
	if account == nil {
		return nil, nil, false, fmt.Errorf("authentication required")
	}
	if !account.Active {
		return nil, nil, false, fmt.Errorf("service account is disabled")
	}
	if account.ExpiresAt != nil && !account.ExpiresAt.After(time.Now().UTC()) {
		return nil, nil, false, fmt.Errorf("service account key is expired")
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
	return serviceAccountAllowsScopes(account, requiredServiceAccountScopes(r))
}

func serviceAccountAllowsScopes(account *AuthenticatedServiceAccount, required []string) bool {
	if account == nil {
		return true
	}
	if len(required) == 0 {
		return true
	}
	scopes := normalizeAuthenticatedServiceAccountScopes(account.Scopes)
	for _, want := range required {
		allowed := false
		for _, scope := range scopes {
			if scope == "*" || scope == "automation:*" || scope == want {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

func requiredServiceAccountScopes(r *http.Request) []string {
	if r != nil && r.Method == http.MethodPost && r.URL.Path == "/api/v1/library/import" {
		var req LibraryImportRequest
		if err := decodeRequestBody(r, &req); err == nil {
			scopes := serviceAccountScopesForLibraryItems(req.Bundle.Items)
			if len(scopes) > 0 {
				return scopes
			}
		}
	}
	if scope := requiredServiceAccountScope(r); scope != "" {
		return []string{scope}
	}
	return nil
}

func serviceAccountScopesForLibraryItems(items []LibraryBundleItem) []string {
	seen := map[string]struct{}{}
	scopes := make([]string, 0, len(items))
	for _, item := range items {
		scope := serviceAccountScopeForLibraryKind(item.Kind)
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		scopes = append(scopes, scope)
	}
	return scopes
}

type serviceAccountScopeRule struct {
	method string
	exact  string
	prefix string
	scope  string
}

func (r serviceAccountScopeRule) matches(method string, path string) bool {
	if r.method != "" && r.method != method {
		return false
	}
	if r.exact != "" {
		return path == r.exact
	}
	return r.prefix != "" && strings.HasPrefix(path, r.prefix)
}

var serviceAccountScopeRules = []serviceAccountScopeRule{
	{prefix: "/api/v1/auth/service-accounts", scope: "service_accounts:manage"},
	{prefix: "/api/v1/auth/users", scope: "service_accounts:manage"},
	{prefix: "/api/v1/auth/user-activity", scope: "audit:read"},
	{prefix: "/api/v1/auth/access-review", scope: "audit:read"},
	{prefix: "/api/v1/auth/effective-access/principal", scope: "audit:read"},
	{prefix: "/api/v1/auth/audit-events", scope: "audit:read"},
	{method: http.MethodPost, exact: "/api/v1/system/settings", scope: "settings:manage"},
	{method: http.MethodPut, exact: "/api/v1/system/settings", scope: "settings:manage"},
	{method: http.MethodPatch, exact: "/api/v1/system/settings", scope: "settings:manage"},
	{method: http.MethodDelete, exact: "/api/v1/system/settings", scope: "settings:manage"},
	{method: http.MethodPatch, exact: "/api/v1/system/generators/drain", scope: "settings:manage"},
	{prefix: "/api/v1/system/restarts", scope: "settings:manage"},
	{prefix: "/api/v1/system/", scope: "system:read"},
	{method: http.MethodGet, prefix: "/api/v1/results", scope: "results:read"},
	{method: http.MethodHead, prefix: "/api/v1/results", scope: "results:read"},
	{prefix: "/api/v1/results", scope: "results:write"},
	{method: http.MethodGet, prefix: "/api/v1/remotes/", scope: "results:read"},
	{method: http.MethodHead, prefix: "/api/v1/remotes/", scope: "results:read"},
	{prefix: "/api/v1/remotes/", scope: "tests:run"},
	{method: http.MethodGet, prefix: "/api/v1/scripts", scope: "read"},
	{method: http.MethodHead, prefix: "/api/v1/scripts", scope: "read"},
	{prefix: "/api/v1/scripts", scope: "scripts:write"},
	{method: http.MethodGet, prefix: "/api/v1/tests", scope: "results:read"},
	{method: http.MethodHead, prefix: "/api/v1/tests", scope: "results:read"},
	{prefix: "/api/v1/tests", scope: "tests:run"},
	{method: http.MethodGet, prefix: "/api/v1/scenarios", scope: "results:read"},
	{method: http.MethodHead, prefix: "/api/v1/scenarios", scope: "results:read"},
	{prefix: "/api/v1/scenarios", scope: "tests:run"},
	{method: http.MethodGet, prefix: "/api/v1/execution-plans", scope: "results:read"},
	{method: http.MethodHead, prefix: "/api/v1/execution-plans", scope: "results:read"},
	{prefix: "/api/v1/execution-plans", scope: "tests:run"},
	{method: http.MethodGet, prefix: "/api/v1/capacity/", scope: "results:read"},
	{method: http.MethodHead, prefix: "/api/v1/capacity/", scope: "results:read"},
	{prefix: "/api/v1/capacity/", scope: "tests:run"},
	{method: http.MethodGet, prefix: "/api/v1/data-pools", scope: "read"},
	{method: http.MethodHead, prefix: "/api/v1/data-pools", scope: "read"},
	{prefix: "/api/v1/data-pools", scope: "scripts:write"},
	{method: http.MethodGet, prefix: "/api/v1/object-repositories", scope: "read"},
	{method: http.MethodHead, prefix: "/api/v1/object-repositories", scope: "read"},
	{prefix: "/api/v1/object-repositories", scope: "scripts:write"},
	{method: http.MethodGet, prefix: "/api/v1/correlation-rulesets", scope: "read"},
	{method: http.MethodHead, prefix: "/api/v1/correlation-rulesets", scope: "read"},
	{prefix: "/api/v1/correlation-rulesets", scope: "scripts:write"},
}

func requiredServiceAccountScope(r *http.Request) string {
	path := r.URL.Path
	method := r.Method
	if requirement, ok := accessRequirementForRequest(r); ok {
		if requirement.ServiceAccountScopeLookup != nil {
			if scope := strings.TrimSpace(requirement.ServiceAccountScopeLookup(r)); scope != "" {
				return scope
			}
		}
		if scope := strings.TrimSpace(requirement.ServiceAccountScope); scope != "" {
			return scope
		}
	}
	if strings.HasPrefix(path, "/api/v1/library") {
		return requiredLibraryServiceAccountScope(r)
	}
	return staticServiceAccountScopeForRoute(method, path)
}

func staticServiceAccountScopeForRoute(method string, path string) string {
	for _, rule := range serviceAccountScopeRules {
		if rule.matches(method, path) {
			return rule.scope
		}
	}
	if method == http.MethodGet || method == http.MethodHead {
		return "read"
	}
	return "write"
}

func requiredLibraryServiceAccountScope(r *http.Request) string {
	path := r.URL.Path
	method := r.Method
	if method == http.MethodGet || method == http.MethodHead || path == "/api/v1/library/export" || strings.HasSuffix(path, "/diff") {
		return "results:read"
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

// normalizeAuthenticatedServiceAccountScopes de-duplicates a service account's stored
// scopes for the auth path. Empty scopes are treated as DENY (an empty set), not
// allow-all: an account with no scopes can only reach unscoped routes, never mutate.
// Real accounts always carry explicit scopes (creation requires at least one), so this
// only fail-closes accounts whose scope set is genuinely empty.
func normalizeAuthenticatedServiceAccountScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
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
