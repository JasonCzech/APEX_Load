package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
	"golang.org/x/crypto/bcrypt"
)

const (
	localSessionCookieName       = "apexload_local_session"
	localSessionTTL              = 12 * time.Hour
	localLoginRateLimitPerMinute = 10
	localInvalidCredentials      = "invalid username or password"
	localAuthStartupTimeout      = 10 * time.Second
)

type LocalLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LocalLoginResponse struct {
	Authenticated bool `json:"authenticated"`
}

type localSessionResolution struct {
	User      *AuthenticatedUser
	Session   *storage.LocalAuthSession
	Token     string
	TokenHash string
	Status    string
}

type localAuthStore interface {
	EnsureLocalAuthFixtures(fixtures []storage.LocalAuthFixture) error
	GetLocalAuthCredential(username string) (*storage.LocalAuthCredential, error)
	SaveLocalAuthSessionForGeneration(generation string, session *storage.LocalAuthSession) error
	GetLocalAuthSessionByTokenHashForGeneration(generation, tokenHash string) (*storage.LocalAuthSession, error)
	DeleteLocalAuthSessionByTokenHash(tokenHash string) error
	DeleteExpiredLocalAuthSessions(cutoff time.Time) error
	ActivateLocalAuthGenerationContext(ctx context.Context, generation string, activatedAt time.Time) (int64, error)
	GetLocalAuthGeneration() (string, error)
}

func localSessionTokenHashForGeneration(generation, token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(generation) + "\x00" + strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func localLogoutCSRFToken(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("apexload:local:logout"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func resolveLocalSession(r *http.Request, store storage.Store, expectedGeneration ...string) localSessionResolution {
	localStore, ok := store.(localAuthStore)
	if store == nil || !ok {
		return localSessionResolution{Status: "unavailable"}
	}
	cookie, err := r.Cookie(localSessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return localSessionResolution{Status: "missing"}
	}
	token := strings.TrimSpace(cookie.Value)
	generation, err := localStore.GetLocalAuthGeneration()
	if err != nil || strings.TrimSpace(generation) == "" {
		return localSessionResolution{Token: token, Status: "unavailable"}
	}
	if len(expectedGeneration) > 0 {
		expected := strings.TrimSpace(expectedGeneration[0])
		if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(generation)) != 1 {
			// A superseded controller must never authenticate the replacement's
			// session, but logout on that surface must remain idempotently able to
			// clear its stale browser cookie or desktop capability binding. Hash
			// against this controller's immutable generation so deletion cannot
			// target a replacement-generation session.
			tokenHash := ""
			if expected != "" {
				tokenHash = localSessionTokenHashForGeneration(expected, token)
			}
			return localSessionResolution{Token: token, TokenHash: tokenHash, Status: "invalid"}
		}
	}
	tokenHash := localSessionTokenHashForGeneration(generation, token)
	session, err := localStore.GetLocalAuthSessionByTokenHashForGeneration(generation, tokenHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return localSessionResolution{Token: token, TokenHash: tokenHash, Status: "invalid"}
		}
		return localSessionResolution{Token: token, TokenHash: tokenHash, Status: "unavailable"}
	}
	if !time.Now().UTC().Before(session.ExpiresAt.UTC()) {
		return localSessionResolution{Session: session, Token: token, TokenHash: tokenHash, Status: "expired"}
	}
	user, err := store.GetUser(session.UserID)
	if err != nil || user == nil {
		return localSessionResolution{Session: session, Token: token, TokenHash: tokenHash, Status: "invalid"}
	}
	if !user.Active {
		return localSessionResolution{Session: session, Token: token, TokenHash: tokenHash, Status: "disabled"}
	}
	if session.SessionEpoch < user.SessionEpoch {
		return localSessionResolution{Session: session, Token: token, TokenHash: tokenHash, Status: "revoked"}
	}
	// Record activity like the OIDC and trusted-header paths do, so Access
	// review does not flag every local user as "never signed in".
	now := time.Now().UTC()
	user.LastSeenAt = &now
	if shouldUpdateUserIdentitySeen(user.ID, user.DisplayName, user.Email, user.DisplayName, user.Email, now) {
		// Activity is not a modification: keep updated_at as it is.
		if err := store.UpdateUserIdentitySeen(user.ID, user.DisplayName, user.Email, now, user.UpdatedAt); err != nil {
			slog.Warn("failed to persist local user last-seen update", "user_id", user.ID, "error", err)
		}
	}
	resolved := authenticatedUserFromStoredUser(user)
	resolved.SessionScoped = true
	resolved.SessionEpoch = session.SessionEpoch
	resolved.SessionExpiresAt = session.ExpiresAt.UTC()
	resolved.LocalScopeBoundViewer = true
	return localSessionResolution{User: resolved, Session: session, Token: token, TokenHash: tokenHash, Status: "active"}
}

func authenticatedUserFromLocalSession(r *http.Request, store storage.Store, expectedGeneration ...string) (*AuthenticatedUser, *storage.LocalAuthSession, error) {
	resolution := resolveLocalSession(r, store, expectedGeneration...)
	if resolution.User == nil {
		switch resolution.Status {
		case "disabled":
			return nil, resolution.Session, fmt.Errorf("user is disabled")
		case "revoked":
			return nil, resolution.Session, fmt.Errorf("session revoked")
		default:
			return nil, resolution.Session, fmt.Errorf("authentication required")
		}
	}
	return resolution.User, resolution.Session, nil
}

func (s *Server) ensureLocalAuthFixtures() error {
	if s == nil || s.store == nil {
		return fmt.Errorf("storage is not configured")
	}
	localStore, ok := s.store.(localAuthStore)
	if !ok {
		return fmt.Errorf("storage backend does not support local test login")
	}
	now := time.Now().UTC()
	type persona struct {
		username, subject, displayName, email string
		bindings                              [][3]string
	}
	personas := []persona{
		{"admin", "local:admin", "Local System Administrator", "admin@apexload.local", [][3]string{{"global", "", RoleSystemAdmin}}},
		{"domainadmin", "local:domainadmin", "Local Domain Administrator", "domainadmin@apexload.local", [][3]string{{"global", "", RoleViewer}, {"domain", storage.DefaultDomainID, RoleProjectAdmin}}},
		{"user", "local:user", "Local Viewer", "user@apexload.local", [][3]string{{"global", "", RoleViewer}, {"domain", storage.DefaultDomainID, RoleViewer}, {"application_repository", storage.DefaultApplicationRepositoryID, RoleViewer}, {"project", storage.DefaultProjectID, RoleViewer}}},
		{"dev", "local:dev", "Local Developer", "dev@apexload.local", [][3]string{{"global", "", RoleViewer}, {"domain", storage.DefaultDomainID, RoleEditor}, {"application_repository", storage.DefaultApplicationRepositoryID, RoleEditor}, {"project", storage.DefaultProjectID, RoleEditor}}},
	}
	fixtures := make([]storage.LocalAuthFixture, 0, len(personas))
	for _, persona := range personas {
		hash, err := bcrypt.GenerateFromPassword([]byte(persona.username), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		userID := "local-auth-" + persona.username
		fixture := storage.LocalAuthFixture{
			Credential: storage.LocalAuthCredential{Username: persona.username, UserID: userID, PasswordHash: string(hash), CreatedAt: now, UpdatedAt: now},
			User:       storage.User{ID: userID, Subject: persona.subject, DisplayName: persona.displayName, Email: persona.email, Active: true, CreatedAt: now, UpdatedAt: now},
		}
		for index, values := range persona.bindings {
			fixture.Bindings = append(fixture.Bindings, &storage.UserRoleBinding{
				ID: fmt.Sprintf("local-auth-%s-binding-%d", persona.username, index+1), UserID: userID,
				ScopeType: values[0], ScopeID: values[1], Role: values[2], CreatedAt: now, UpdatedAt: now,
			})
		}
		fixtures = append(fixtures, fixture)
	}
	return localStore.EnsureLocalAuthFixtures(fixtures)
}

func (s *Server) activateLocalAuthGenerationForStartupContext(ctx context.Context) (int64, error) {
	if s == nil || s.store == nil {
		return 0, fmt.Errorf("storage is not configured")
	}
	localStore, ok := s.store.(localAuthStore)
	if !ok {
		return 0, fmt.Errorf("storage backend does not support local test login")
	}
	raw := make([]byte, sha256.Size)
	if _, err := rand.Read(raw); err != nil {
		return 0, fmt.Errorf("generate local auth controller generation: %w", err)
	}
	generation := hex.EncodeToString(raw)
	if ctx == nil {
		ctx = context.Background()
	}
	invalidated, err := localStore.ActivateLocalAuthGenerationContext(ctx, generation, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	s.localAuthGenerationMu.Lock()
	s.localAuthGeneration = generation
	s.localAuthGenerationMu.Unlock()
	return invalidated, nil
}

func (s *Server) activateLocalAuthGenerationForStartup() (int64, error) {
	return s.activateLocalAuthGenerationForStartupContext(context.Background())
}

func (s *Server) activeLocalAuthGeneration() string {
	if s == nil {
		return ""
	}
	s.localAuthGenerationMu.RLock()
	defer s.localAuthGenerationMu.RUnlock()
	return s.localAuthGeneration
}

func (s *Server) allowLocalLoginAttempt(w http.ResponseWriter, r *http.Request, username string) bool {
	if s == nil || s.cache == nil {
		return true
	}
	peer := "unknown"
	if ip := requestSourceIP(r); ip != nil {
		peer = ip.String()
	}
	keySum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	key := "local-login:" + peer + ":" + hex.EncodeToString(keySum[:8])
	allowed, remaining, err := s.cache.AllowRateLimit(r.Context(), key, localLoginRateLimitPerMinute, time.Minute)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "local login rate limit is unavailable")
		return false
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(localLoginRateLimitPerMinute))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "local login rate limit exceeded")
		return false
	}
	return true
}

// OpenAPI-Responses: 429
func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings := s.authSettingsForRequest()
	if settings.Mode != authModeLocal || productionMode() {
		writeError(w, http.StatusNotFound, "local login is not enabled")
		return
	}
	localStore, ok := s.store.(localAuthStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "local login storage is unavailable")
		return
	}
	generation := s.activeLocalAuthGeneration()
	currentGeneration, err := localStore.GetLocalAuthGeneration()
	if err != nil || generation == "" || subtle.ConstantTimeCompare([]byte(generation), []byte(currentGeneration)) != 1 {
		writeError(w, http.StatusServiceUnavailable, "local login is restarting; reload and try again")
		return
	}
	var request LocalLoginRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	username := strings.ToLower(strings.TrimSpace(request.Username))
	if !s.allowLocalLoginAttempt(w, r, username) {
		return
	}
	credential, credentialErr := localStore.GetLocalAuthCredential(username)
	// This is a valid bcrypt hash for a value no fixture uses. Comparing it for
	// unknown usernames keeps the failure path aligned with known-user failures.
	passwordHash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	if credentialErr == nil && credential != nil {
		passwordHash = credential.PasswordHash
	}
	passwordOK := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(request.Password)) == nil
	if credentialErr != nil || credential == nil || !passwordOK {
		s.recordAuditEvent(r, "auth.local.login", "failure", "auth_session", "", "", map[string]any{"reason": "invalid_credentials"})
		writeError(w, http.StatusUnauthorized, localInvalidCredentials)
		return
	}
	user, err := s.store.GetUser(credential.UserID)
	if err != nil || user == nil || !user.Active {
		s.recordAuditEvent(r, "auth.local.login", "failure", "auth_session", "", "", map[string]any{"reason": "invalid_credentials"})
		writeError(w, http.StatusUnauthorized, localInvalidCredentials)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create local session")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now().UTC()
	session := &storage.LocalAuthSession{TokenHash: localSessionTokenHashForGeneration(generation, token), UserID: user.ID, SessionEpoch: user.SessionEpoch, CreatedAt: now, ExpiresAt: now.Add(localSessionTTL)}
	_ = localStore.DeleteExpiredLocalAuthSessions(now)
	if err := localStore.SaveLocalAuthSessionForGeneration(generation, session); err != nil {
		if errors.Is(err, storage.ErrLocalAuthGenerationChanged) {
			writeError(w, http.StatusServiceUnavailable, "local login is restarting; reload and try again")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create local session")
		return
	}
	if s.desktopMode {
		capability := desktopBrowserCapabilityFromRequest(r)
		if !s.bindDesktopLocalSession(capability, token) {
			_ = localStore.DeleteLocalAuthSessionByTokenHash(session.TokenHash)
			writeError(w, http.StatusUnauthorized, "desktop capability is missing or invalid")
			return
		}
	} else {
		setLocalAuthCookie(w, r, settings, &http.Cookie{Name: localSessionCookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: session.ExpiresAt, MaxAge: int(localSessionTTL.Seconds())})
	}
	resolved := authenticatedUserFromStoredUser(user)
	s.recordAuditEvent(withAuthenticatedContext(r, authResolution{User: resolved}), "auth.local.login", "success", "auth_session", user.Subject, "", nil)
	writeJSON(w, http.StatusOK, LocalLoginResponse{Authenticated: true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	settings := s.authSettingsForRequest()
	if settings.Mode == authModeLocal {
		s.handleLocalLogout(w, r)
		return
	}
	s.handleOIDCLogout(w, r)
}

func (s *Server) handleLocalLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	settings := s.authSettingsForRequest()
	if settings.Mode != authModeLocal {
		writeError(w, http.StatusNotFound, "local logout is not enabled")
		return
	}
	resolution := resolveLocalSession(r, s.store, s.activeLocalAuthGeneration())
	if resolution.Token == "" {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	localStore, ok := s.store.(localAuthStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "local login storage is unavailable")
		return
	}
	provided := strings.TrimSpace(r.Header.Get("X-APEXLoad-CSRF-Token"))
	expected := localLogoutCSRFToken(resolution.Token)
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		writeError(w, http.StatusForbidden, "invalid csrf token")
		return
	}
	tokenHash := resolution.TokenHash
	if tokenHash == "" {
		writeError(w, http.StatusServiceUnavailable, "local login storage is unavailable")
		return
	}
	if err := localStore.DeleteLocalAuthSessionByTokenHash(tokenHash); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to end local session")
		return
	}
	auditUser := resolution.User
	if auditUser == nil && resolution.Session != nil {
		if storedUser, err := s.store.GetUser(resolution.Session.UserID); err == nil && storedUser != nil {
			auditUser = authenticatedUserFromStoredUser(storedUser)
		}
	}
	auditSubject := ""
	if auditUser != nil {
		auditSubject = auditUser.Subject
	}
	s.recordAuditEvent(withAuthenticatedContext(r, authResolution{User: auditUser}), "auth.local.logout", "success", "auth_session", auditSubject, "", nil)
	if s.desktopMode {
		s.clearDesktopLocalSession(desktopBrowserCapabilityFromRequest(r))
	} else {
		clearLocalAuthCookie(w, r, settings, localSessionCookieName, "/")
	}
	writeJSON(w, http.StatusOK, AuthLogoutResponse{RedirectURL: "/"})
}

// localAuthCookieSecure derives the cookie transport policy from the request
// that actually reached the controller. Local Login does not use the OIDC
// redirect URL, so a retained OIDC setting must not make its cookie unusable on
// HTTP or weaken it on HTTPS. Forwarded transport metadata is honored only
// when the immediate peer passes the configured trusted-proxy gate.
func localAuthCookieSecure(r *http.Request, settings AuthSettings) bool {
	if r == nil {
		return false
	}
	if trustedProxyGateConfigured(settings) && trustedProxyRequestAllowed(r, settings) {
		switch githubForwardedProto(r.Header.Get("X-Forwarded-Proto")) {
		case "https":
			return true
		case "http":
			return false
		}
	}
	return r.TLS != nil
}

func setLocalAuthCookie(w http.ResponseWriter, r *http.Request, settings AuthSettings, cookie *http.Cookie) {
	cookie.Secure = localAuthCookieSecure(r, settings)
	http.SetCookie(w, cookie)
}

func clearLocalAuthCookie(w http.ResponseWriter, r *http.Request, settings AuthSettings, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   localAuthCookieSecure(r, settings),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}
