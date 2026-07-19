package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	githubStateCookieName = "apexload_github_state"
	githubStateTTL        = 10 * time.Minute
)

type githubStateToken struct {
	State     string    `json:"state"`
	UserID    string    `json:"user_id"`
	ReturnTo  string    `json:"return_to"`
	ExpiresAt time.Time `json:"exp"`
	IssuedAt  time.Time `json:"iat"`
}

func githubCookieSecret() string {
	return strings.TrimSpace(os.Getenv(githubTokenKeyEnvVar))
}

func (s *Server) currentGitHubSettings() GitHubSettings {
	return normalizeGitHubSettings(s.runner.GetSystemSettings().GitHub)
}

func (s *Server) githubConnectionStore() (storage.GitHubConnectionStore, bool) {
	store, ok := s.store.(storage.GitHubConnectionStore)
	return store, ok
}

// githubUserKey returns the stable id used to key a user's GitHub connection. In
// dev/no-auth modes where no user is present, a single synthetic "local" user is
// used (documented behavior).
func githubUserKey(r *http.Request) string {
	if user, ok := AuthenticatedUserFromContext(r.Context()); ok && user != nil {
		if subject := strings.TrimSpace(user.Subject); subject != "" {
			return subject
		}
		if id := strings.TrimSpace(user.ID); id != "" {
			return id
		}
	}
	return "local"
}

func (s *Server) githubCallbackURL(r *http.Request) string {
	if s != nil && s.desktopMode {
		callbackURL, err := s.desktopLoopbackCallbackURL("/api/v1/integrations/github/callback")
		if err != nil {
			return ""
		}
		return callbackURL
	}
	scheme := "https"
	if forwarded := githubForwardedProto(r.Header.Get("X-Forwarded-Proto")); forwarded == "https" || (forwarded == "http" && !productionMode()) {
		scheme = forwarded
	} else if r.TLS == nil && !productionMode() {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/api/v1/integrations/github/callback"
}

func githubForwardedProto(raw string) string {
	if raw == "" {
		return ""
	}
	if idx := strings.IndexByte(raw, ','); idx >= 0 {
		raw = raw[:idx]
	}
	switch proto := strings.ToLower(strings.TrimSpace(raw)); proto {
	case "http", "https":
		return proto
	default:
		return ""
	}
}

// handleGitHubConfig returns the non-secret global integration config plus
// readiness flags for the dashboard.
func (s *Server) handleGitHubConfig(w http.ResponseWriter, r *http.Request) {
	settings := s.currentGitHubSettings()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":              settings.Enabled,
		"oauth_configured":     settings.oauthConfigured(),
		"app_configured":       settings.appConfigured(),
		"webhook_configured":   settings.webhookConfigured(),
		"pat_configured":       settings.patConfigured(),
		"encryption_available": githubTokenEncryptionAvailable(),
		"app_id":               settings.AppID,
		"app_slug":             settings.AppSlug,
		"automation_mode":      settings.AutomationMode,
		"checks_enabled":       settings.ChecksEnabled,
		"api_base_url":         settings.APIBaseURL,
		"web_base_url":         settings.WebBaseURL,
		"scopes":               settings.Scopes,
		"default_repo":         settings.DefaultRepo,
		"default_branch":       settings.DefaultBranch,
		"path_template":        settings.PathTemplate,
	})
}

func (s *Server) githubFeatureGuard(w http.ResponseWriter, settings GitHubSettings) bool {
	if !settings.Enabled {
		writeError(w, http.StatusNotFound, "github integration is not enabled")
		return false
	}
	if !settings.oauthConfigured() && !settings.appConfigured() && !settings.patConfigured() {
		writeError(w, http.StatusServiceUnavailable, "github authentication is not configured")
		return false
	}
	if settings.oauthConfigured() && !githubTokenEncryptionAvailable() {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("github token encryption key (%s) is not configured", githubTokenKeyEnvVar))
		return false
	}
	if settings.oauthConfigured() {
		if _, ok := s.githubConnectionStore(); !ok {
			writeError(w, http.StatusServiceUnavailable, "github connection storage is unavailable")
			return false
		}
	}
	return true
}

func (s *Server) handleGitHubLogin(w http.ResponseWriter, r *http.Request) {
	settings := s.currentGitHubSettings()
	if !s.githubFeatureGuard(w, settings) {
		return
	}
	if !settings.oauthConfigured() {
		writeError(w, http.StatusServiceUnavailable, "github oauth app is not configured")
		return
	}
	callbackURL := s.githubCallbackURL(r)
	if callbackURL == "" {
		writeError(w, http.StatusServiceUnavailable, "desktop API callback is unavailable")
		return
	}
	state, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to initialize github state")
		return
	}
	now := time.Now().UTC()
	loginState := githubStateToken{
		State:     state,
		UserID:    githubUserKey(r),
		ReturnTo:  sanitizeReturnTo(r.URL.Query().Get("return_to")),
		ExpiresAt: now.Add(githubStateTTL),
		IssuedAt:  now,
	}
	if s.desktopMode {
		capability, ok := s.requireDesktopBrowserCapability(w, r)
		if !ok {
			return
		}
		stored, storeErr := s.storeDesktopGitHubLoginState(capability, r, loginState)
		if storeErr != nil {
			writeError(w, http.StatusBadRequest, storeErr.Error())
			return
		}
		if !stored {
			writeError(w, http.StatusServiceUnavailable, "desktop github authentication state capacity is unavailable")
			return
		}
	} else {
		encoded, err := encodeSignedCookieValue(loginState, githubCookieSecret())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to initialize github state")
			return
		}
		s.setSignedAuthCookie(w, normalizeAuthSettings(s.runner.GetSystemSettings().Auth), &http.Cookie{
			Name:     githubStateCookieName,
			Value:    encoded,
			Path:     "/api/v1/integrations/github",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  now.Add(githubStateTTL),
			MaxAge:   int(githubStateTTL.Seconds()),
		})
	}

	authorize := url.Values{}
	authorize.Set("client_id", settings.ClientID)
	authorize.Set("redirect_uri", callbackURL)
	authorize.Set("scope", strings.Join(settings.Scopes, " "))
	authorize.Set("state", state)
	writeAuthNavigation(w, r, settings.WebBaseURL+"/login/oauth/authorize?"+authorize.Encode())
}

func (s *Server) desktopGitHubCallbackLanding(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.desktopMode {
			writeDesktopOAuthCallbackLanding(w, "Completing GitHub connection")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	settings := s.currentGitHubSettings()
	if !s.githubFeatureGuard(w, settings) {
		return
	}
	if !settings.oauthConfigured() {
		writeError(w, http.StatusServiceUnavailable, "github oauth app is not configured")
		return
	}
	var state githubStateToken
	if s.desktopMode {
		capability, ok := s.requireDesktopBrowserCapability(w, r)
		if !ok {
			return
		}
		state, ok = s.takeDesktopGitHubState(capability, subtleQueryValue(r.URL.Query().Get("state")))
		if !ok {
			writeError(w, http.StatusUnauthorized, "github authentication state is invalid, expired, or belongs to another browser tab")
			return
		}
	} else {
		stateCookie, err := r.Cookie(githubStateCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "github authentication state is missing")
			return
		}
		if err := decodeSignedCookieValue(stateCookie.Value, githubCookieSecret(), &state); err != nil ||
			state.State == "" || state.IssuedAt.IsZero() || time.Now().UTC().After(state.ExpiresAt) {
			writeError(w, http.StatusUnauthorized, "github authentication state is invalid or expired")
			return
		}
	}
	if subtleQueryValue(r.URL.Query().Get("state")) != state.State {
		writeError(w, http.StatusUnauthorized, "github authentication state mismatch")
		return
	}
	// The signed state is bound to the local identity that initiated account
	// linking. Refuse to attach the resulting GitHub credential if the local
	// browser session changed accounts before the callback completed.
	if currentUserID := githubUserKey(r); currentUserID != strings.TrimSpace(state.UserID) {
		writeError(w, http.StatusUnauthorized, "github authentication state belongs to a different local user")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "github authorization code is required")
		return
	}

	tokenResp, err := exchangeGitHubOAuthCode(r.Context(), settings, code, s.githubCallbackURL(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, "github token exchange failed")
		return
	}
	client, err := newGitHubClient(settings, tokenResp.AccessToken, 15*time.Second)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	identity, err := client.Identity(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to resolve github identity")
		return
	}

	encAccess, err := encryptGitHubToken(tokenResp.AccessToken)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to secure github token")
		return
	}
	encRefresh, err := encryptGitHubToken(tokenResp.RefreshToken)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "failed to secure github token")
		return
	}
	expiresAt, err := githubOAuthExpiresAt(time.Now().UTC(), tokenResp.ExpiresIn)
	if err != nil {
		writeError(w, http.StatusBadGateway, "github token exchange returned an invalid expiry")
		return
	}

	store, _ := s.githubConnectionStore()
	guardedStore, ok := store.(interface {
		SaveGitHubConnectionIfNotRevokedSince(*storage.GitHubConnection, time.Time) (bool, error)
	})
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github connection storage does not support revocation fencing")
		return
	}
	saved, err := guardedStore.SaveGitHubConnectionIfNotRevokedSince(&storage.GitHubConnection{
		UserID:          state.UserID,
		GitHubLogin:     identity.Login,
		GitHubUserID:    fmt.Sprint(identity.ID),
		AccessTokenEnc:  encAccess,
		RefreshTokenEnc: encRefresh,
		TokenType:       tokenResp.TokenType,
		Scopes:          tokenResp.Scope,
		ExpiresAt:       expiresAt,
	}, state.IssuedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist github connection")
		return
	}
	if !saved {
		if !s.desktopMode {
			s.clearAuthCookie(w, normalizeAuthSettings(s.runner.GetSystemSettings().Auth), githubStateCookieName, "/api/v1/integrations/github")
		}
		writeError(w, http.StatusConflict, "github connection flow was canceled by a newer disconnect")
		return
	}
	s.recordAuditEvent(r, "github.connection.create", "success", "github_connection", state.UserID, "", map[string]any{"login": identity.Login})

	if s.desktopMode {
		writeJSON(w, http.StatusOK, map[string]string{"redirect_url": sanitizeReturnTo(state.ReturnTo)})
		return
	}
	// Clear the hosted state cookie and return to the dashboard.
	s.clearAuthCookie(w, normalizeAuthSettings(s.runner.GetSystemSettings().Auth), githubStateCookieName, "/api/v1/integrations/github")
	http.Redirect(w, r, sanitizeReturnTo(state.ReturnTo), http.StatusFound)
}

func (s *Server) handleGitHubConnection(w http.ResponseWriter, r *http.Request) {
	store, ok := s.githubConnectionStore()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	conn, err := store.GetGitHubConnection(githubUserKey(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read github connection")
		return
	}
	if conn == nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	resp := map[string]any{
		"connected":      true,
		"login":          conn.GitHubLogin,
		"github_user_id": conn.GitHubUserID,
		"scopes":         conn.Scopes,
		"connected_at":   conn.ConnectedAt,
	}
	if conn.ExpiresAt != nil {
		resp["expires_at"] = conn.ExpiresAt
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGitHubDisconnect(w http.ResponseWriter, r *http.Request) {
	store, ok := s.githubConnectionStore()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"disconnected": true})
		return
	}
	userKey := githubUserKey(r)
	if err := store.DeleteGitHubConnection(userKey); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to disconnect github")
		return
	}
	// Cancel any not-yet-completed OAuth linking flow in this browser as part of
	// the disconnect/revocation intent.
	if s.desktopMode {
		s.clearDesktopGitHubState(desktopBrowserCapabilityFromRequest(r))
	} else {
		s.clearAuthCookie(w, normalizeAuthSettings(s.runner.GetSystemSettings().Auth), githubStateCookieName, "/api/v1/integrations/github")
	}
	s.recordAuditEvent(r, "github.connection.delete", "success", "github_connection", userKey, "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"disconnected": true})
}
