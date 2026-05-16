package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcSessionCookieName = "stormrunner_auth_session"
	oidcStateCookieName   = "stormrunner_auth_state"
	oidcSessionTTL        = 12 * time.Hour
	oidcStateTTL          = 10 * time.Minute
	oidcLoginPath         = "/api/v1/auth/login"
	oidcLogoutPath        = "/api/v1/auth/logout"
	defaultOIDCReturnTo   = "/"
)

type oidcSessionToken struct {
	Subject      string              `json:"sub"`
	DisplayName  string              `json:"name,omitempty"`
	Email        string              `json:"email,omitempty"`
	GlobalRoles  []string            `json:"global_roles,omitempty"`
	ProjectRoles map[string][]string `json:"project_roles,omitempty"`
	ExpiresAt    time.Time           `json:"exp"`
	IssuedAt     time.Time           `json:"iat"`
}

type oidcStateToken struct {
	State    string    `json:"state"`
	Nonce    string    `json:"nonce"`
	ReturnTo string    `json:"return_to"`
	ExpiresAt time.Time `json:"exp"`
	IssuedAt  time.Time `json:"iat"`
}

func authStatusURLs(settings AuthSettings) (string, string) {
	if settings.Mode == authModeOIDC {
		return oidcLoginPath, oidcLogoutPath
	}
	return settings.LoginURL, settings.LogoutURL
}

func authenticatedUserFromOIDCSession(r *http.Request, settings AuthSettings) (*AuthenticatedUser, error) {
	secret, err := authSessionSecret(settings)
	if err != nil {
		return nil, fmt.Errorf("authentication required")
	}
	cookie, err := r.Cookie(oidcSessionCookieName)
	if err != nil {
		return nil, fmt.Errorf("authentication required")
	}
	var session oidcSessionToken
	if err := decodeSignedCookieValue(cookie.Value, secret, &session); err != nil {
		return nil, fmt.Errorf("authentication required")
	}
	if session.Subject == "" || time.Now().UTC().After(session.ExpiresAt) {
		return nil, fmt.Errorf("authentication required")
	}
	return &AuthenticatedUser{
		Subject:      session.Subject,
		DisplayName:  session.DisplayName,
		Email:        session.Email,
		GlobalRoles:  sliceToRoleSet(session.GlobalRoles),
		ProjectRoles: mapToProjectRoleSet(session.ProjectRoles),
	}, nil
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	if settings.Mode != authModeOIDC {
		writeError(w, http.StatusNotFound, "oidc login is not enabled")
		return
	}
	runtime, err := newOIDCRuntime(r.Context(), settings)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	state, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to initialize oidc state")
		return
	}
	nonce, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to initialize oidc nonce")
		return
	}
	now := time.Now().UTC()
	encodedState, err := encodeSignedCookieValue(oidcStateToken{
		State:    state,
		Nonce:    nonce,
		ReturnTo: sanitizeReturnTo(r.URL.Query().Get("return_to")),
		ExpiresAt: now.Add(oidcStateTTL),
		IssuedAt:  now,
	}, runtime.sessionSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to initialize oidc state")
		return
	}
	setSignedAuthCookie(w, settings, &http.Cookie{
		Name:     oidcStateCookieName,
		Value:    encodedState,
		Path:     "/api/v1/auth",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  now.Add(oidcStateTTL),
		MaxAge:   int(oidcStateTTL.Seconds()),
	})
	http.Redirect(w, r, runtime.oauth2Config.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	if settings.Mode != authModeOIDC {
		writeError(w, http.StatusNotFound, "oidc callback is not enabled")
		return
	}
	runtime, err := newOIDCRuntime(r.Context(), settings)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	stateCookie, err := r.Cookie(oidcStateCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var loginState oidcStateToken
	if err := decodeSignedCookieValue(stateCookie.Value, runtime.sessionSecret, &loginState); err != nil || loginState.State == "" || time.Now().UTC().After(loginState.ExpiresAt) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if subtleQueryValue(r.URL.Query().Get("state")) != loginState.State {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "authorization code is required")
		return
	}
	token, err := runtime.oauth2Config.Exchange(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "oidc token exchange failed")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		writeError(w, http.StatusUnauthorized, "oidc id_token is missing")
		return
	}
	idToken, err := runtime.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "oidc id_token verification failed")
		return
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		writeError(w, http.StatusUnauthorized, "oidc id_token claims are invalid")
		return
	}
	if strings.TrimSpace(stringClaim(claims["nonce"])) != loginState.Nonce {
		writeError(w, http.StatusUnauthorized, "oidc nonce verification failed")
		return
	}
	user, session, err := authenticatedUserFromOIDCClaims(claims, idToken.Expiry, settings)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	encodedSession, err := encodeSignedCookieValue(session, runtime.sessionSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create oidc session")
		return
	}
	setSignedAuthCookie(w, settings, &http.Cookie{
		Name:     oidcSessionCookieName,
		Value:    encodedSession,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  session.ExpiresAt,
		MaxAge:   maxInt(1, int(time.Until(session.ExpiresAt).Seconds())),
	})
	clearAuthCookie(w, settings, oidcStateCookieName, "/api/v1/auth")
	s.recordAuditEvent(withAuthenticatedContext(r, authResolution{User: user}), "auth.oidc.login", "success", "auth_session", user.Subject, "", nil)
	http.Redirect(w, r, loginState.ReturnTo, http.StatusSeeOther)
}

func (s *Server) handleOIDCLogout(w http.ResponseWriter, r *http.Request) {
	settings := normalizeAuthSettings(s.runner.GetSystemSettings().Auth)
	if settings.Mode != authModeOIDC {
		writeError(w, http.StatusNotFound, "oidc logout is not enabled")
		return
	}
	if user, err := authenticatedUserFromOIDCSession(r, settings); err == nil {
		s.recordAuditEvent(withAuthenticatedContext(r, authResolution{User: user}), "auth.oidc.logout", "success", "auth_session", user.Subject, "", nil)
	}
	clearAuthCookie(w, settings, oidcSessionCookieName, "/")
	clearAuthCookie(w, settings, oidcStateCookieName, "/api/v1/auth")
	destination := settings.LogoutURL
	if strings.TrimSpace(destination) == "" {
		destination = defaultOIDCReturnTo
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}

type oidcRuntime struct {
	oauth2Config  oauth2.Config
	verifier      *oidc.IDTokenVerifier
	sessionSecret string
}

func newOIDCRuntime(ctx context.Context, settings AuthSettings) (*oidcRuntime, error) {
	clientSecret := strings.TrimSpace(os.Getenv(settings.ClientSecretEnvVar))
	if clientSecret == "" {
		return nil, fmt.Errorf("oidc client secret environment variable %q is not set", settings.ClientSecretEnvVar)
	}
	sessionSecret, err := authSessionSecret(settings)
	if err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(ctx, settings.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery failed: %w", err)
	}
	return &oidcRuntime{
		oauth2Config: oauth2.Config{
			ClientID:     settings.ClientID,
			ClientSecret: clientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  settings.RedirectURL,
			Scopes:       append([]string(nil), settings.Scopes...),
		},
		verifier:      provider.Verifier(&oidc.Config{ClientID: settings.ClientID}),
		sessionSecret: sessionSecret,
	}, nil
}

func authenticatedUserFromOIDCClaims(claims map[string]any, expiry time.Time, settings AuthSettings) (*AuthenticatedUser, oidcSessionToken, error) {
	subject := strings.TrimSpace(stringClaim(claims["sub"]))
	if subject == "" {
		return nil, oidcSessionToken{}, fmt.Errorf("oidc subject claim is required")
	}
	globalRoles := normalizeRoleList(anyToStringSlice(claims[settings.RolesClaim]))
	projectRoles := normalizeProjectRoleClaims(claims[settings.ProjectRolesClaim])
	if expiry.IsZero() {
		expiry = time.Now().UTC().Add(oidcSessionTTL)
	}
	maxExpiry := time.Now().UTC().Add(oidcSessionTTL)
	if expiry.After(maxExpiry) {
		expiry = maxExpiry
	}
	session := oidcSessionToken{
		Subject:      subject,
		DisplayName:  strings.TrimSpace(stringClaim(claims["name"])),
		Email:        strings.TrimSpace(stringClaim(claims["email"])),
		GlobalRoles:  globalRoles,
		ProjectRoles: projectRoles,
		IssuedAt:     time.Now().UTC(),
		ExpiresAt:    expiry,
	}
	return &AuthenticatedUser{
		Subject:      session.Subject,
		DisplayName:  session.DisplayName,
		Email:        session.Email,
		GlobalRoles:  sliceToRoleSet(session.GlobalRoles),
		ProjectRoles: mapToProjectRoleSet(session.ProjectRoles),
	}, session, nil
}

func authSessionSecret(settings AuthSettings) (string, error) {
	secret := strings.TrimSpace(os.Getenv(settings.SessionSecretEnvVar))
	if secret == "" {
		return "", fmt.Errorf("oidc session secret environment variable %q is not set", settings.SessionSecretEnvVar)
	}
	return secret, nil
}

func encodeSignedCookieValue(payload any, secret string) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(encodedPayload)); err != nil {
		return "", err
	}
	encodedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + encodedSignature, nil
}

func decodeSignedCookieValue(value string, secret string, out any) error {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return errors.New("invalid signed cookie format")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(parts[0])); err != nil {
		return err
	}
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(actual, expected) {
		return errors.New("invalid signed cookie signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func setSignedAuthCookie(w http.ResponseWriter, settings AuthSettings, cookie *http.Cookie) {
	cookie.Secure = authCookieSecure(settings)
	http.SetCookie(w, cookie)
}

func clearAuthCookie(w http.ResponseWriter, settings AuthSettings, name string, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   authCookieSecure(settings),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}

func authCookieSecure(settings AuthSettings) bool {
	parsed, err := url.Parse(settings.RedirectURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https"
}

func sanitizeReturnTo(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultOIDCReturnTo
	}
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") {
		return defaultOIDCReturnTo
	}
	return trimmed
}

func sliceToRoleSet(roles []string) map[string]struct{} {
	set := make(map[string]struct{}, len(roles))
	for _, role := range normalizeRoleList(roles) {
		set[role] = struct{}{}
	}
	return set
}

func mapToProjectRoleSet(projectRoles map[string][]string) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{}, len(projectRoles))
	for projectID, roles := range projectRoles {
		normalizedProjectID := normalizeProjectID(projectID)
		if normalizedProjectID == "" {
			continue
		}
		roleSet := sliceToRoleSet(roles)
		if len(roleSet) == 0 {
			continue
		}
		out[normalizedProjectID] = roleSet
	}
	return out
}

func normalizeRoleList(roles []string) []string {
	if len(roles) == 0 {
		return nil
	}
	allowed := map[string]struct{}{
		RoleSystemAdmin:  {},
		RoleProjectAdmin: {},
		RoleEditor:       {},
		RoleViewer:       {},
	}
	seen := make(map[string]struct{}, len(roles))
	out := make([]string, 0, len(roles))
	for _, role := range roles {
		normalized := strings.ToLower(strings.TrimSpace(role))
		if normalized == "" {
			continue
		}
		if _, ok := allowed[normalized]; !ok {
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

func normalizeProjectRoleClaims(value any) map[string][]string {
	out := make(map[string][]string)
	switch typed := value.(type) {
	case string:
		return projectRoleSetToList(parseProjectRolesHeader(typed))
	case map[string]any:
		for projectID, rawRoles := range typed {
			roles := normalizeRoleList(anyToStringSlice(rawRoles))
			if len(roles) > 0 {
				out[normalizeProjectID(projectID)] = roles
			}
		}
	case map[string][]string:
		for projectID, roles := range typed {
			normalized := normalizeRoleList(roles)
			if len(normalized) > 0 {
				out[normalizeProjectID(projectID)] = normalized
			}
		}
	}
	return out
}

func anyToStringSlice(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return strings.FieldsFunc(typed, func(r rune) bool {
			switch r {
			case ',', '|', ' ', '\t', '\n':
				return true
			default:
				return false
			}
		})
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if str := strings.TrimSpace(stringClaim(item)); str != "" {
				out = append(out, str)
			}
		}
		return out
	default:
		if str := strings.TrimSpace(stringClaim(typed)); str != "" {
			return []string{str}
		}
		return nil
	}
}

func stringClaim(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func projectRoleSetToList(input map[string]map[string]struct{}) map[string][]string {
	out := make(map[string][]string, len(input))
	for projectID, roles := range input {
		items := make([]string, 0, len(roles))
		for role := range roles {
			items = append(items, role)
		}
		if len(items) > 0 {
			out[projectID] = normalizeRoleList(items)
		}
	}
	return out
}

func subtleQueryValue(value string) string {
	return strings.TrimSpace(value)
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}