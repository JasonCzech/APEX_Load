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
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcSessionCookieName         = "apexload_auth_session"
	oidcStateCookieName           = "apexload_auth_state"
	oidcSessionTTL                = 12 * time.Hour
	oidcStateTTL                  = 10 * time.Minute
	oidcLogoutCSRFPath            = "/api/v1/auth/logout"
	oidcLogoutCSRFTTL             = 10 * time.Minute
	oidcLoginPath                 = "/api/v1/auth/login"
	oidcCallbackPath              = "/api/v1/auth/callback"
	oidcLogoutPath                = "/api/v1/auth/logout"
	defaultOIDCReturnTo           = "/"
	defaultOIDCDiscoveryTimeout   = 10 * time.Second
	defaultOIDCLoginRatePerMinute = 30
	oidcRemoteResponseMaxBytes    = 2 << 20
	oidcResponseHeaderMaxBytes    = 64 << 10
	productionSecretMinBytes      = 32
)

type oidcSessionToken struct {
	UserID          string              `json:"uid,omitempty"`
	Subject         string              `json:"sub"`
	DisplayName     string              `json:"name,omitempty"`
	Email           string              `json:"email,omitempty"`
	GlobalRoles     []string            `json:"global_roles,omitempty"`
	ProjectRoles    map[string][]string `json:"project_roles,omitempty"`
	DomainRoles     map[string][]string `json:"domain_roles,omitempty"`
	RepositoryRoles map[string][]string `json:"application_repository_roles,omitempty"`
	SessionEpoch    int                 `json:"sepoch,omitempty"`
	ExpiresAt       time.Time           `json:"exp"`
	IssuedAt        time.Time           `json:"iat"`
}

type oidcStateToken struct {
	State     string    `json:"state"`
	Nonce     string    `json:"nonce"`
	ReturnTo  string    `json:"return_to"`
	ExpiresAt time.Time `json:"exp"`
	IssuedAt  time.Time `json:"iat"`
}

type oidcLogoutCSRFToken struct {
	Subject   string    `json:"sub"`
	Action    string    `json:"action"`
	ExpiresAt time.Time `json:"exp"`
	IssuedAt  time.Time `json:"iat"`
}

type oidcSessionResolution struct {
	User    *AuthenticatedUser
	Session oidcSessionToken
	Status  string
}

func authStatusURLs(settings AuthSettings) (string, string) {
	if settings.Mode == authModeOIDC || settings.Mode == authModeLocal {
		return oidcLoginPath, oidcLogoutPath
	}
	return settings.LoginURL, settings.LogoutURL
}

func authenticatedUserFromOIDCSession(r *http.Request, settings AuthSettings) (*AuthenticatedUser, error) {
	resolution := resolveOIDCSession(r, settings)
	if resolution.User == nil {
		return nil, fmt.Errorf("authentication required")
	}
	return resolution.User, nil
}

func resolveOIDCSession(r *http.Request, settings AuthSettings) oidcSessionResolution {
	secret, err := authSessionSecret(settings)
	if err != nil {
		return oidcSessionResolution{Status: "unavailable"}
	}
	cookie, err := r.Cookie(oidcSessionCookieName)
	if err != nil {
		return oidcSessionResolution{Status: "missing"}
	}
	var session oidcSessionToken
	if err := decodeSignedCookieValue(cookie.Value, secret, &session); err != nil {
		return oidcSessionResolution{Status: "invalid"}
	}
	if session.Subject == "" {
		return oidcSessionResolution{Session: session, Status: "invalid"}
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		return oidcSessionResolution{Session: session, Status: "expired"}
	}
	return oidcSessionResolution{Status: "active", Session: session, User: &AuthenticatedUser{
		ID:               strings.TrimSpace(session.UserID),
		Subject:          session.Subject,
		DisplayName:      session.DisplayName,
		Email:            session.Email,
		GlobalRoles:      sliceToRoleSet(session.GlobalRoles),
		ProjectRoles:     mapToProjectRoleSet(session.ProjectRoles),
		DomainRoles:      mapToDomainRoleSet(session.DomainRoles),
		RepositoryRoles:  mapToRepositoryRoleSet(session.RepositoryRoles),
		SessionScoped:    true,
		SessionEpoch:     session.SessionEpoch,
		SessionExpiresAt: session.ExpiresAt,
	}}
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	settings := s.authSettingsForRequest()
	if settings.Mode != authModeOIDC {
		writeError(w, http.StatusNotFound, "oidc login is not enabled")
		return
	}
	var err error
	settings, err = s.effectiveOIDCSettings(settings)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if !s.allowOIDCLoginAttempt(w, r) {
		return
	}
	runtime, err := s.cachedOIDCRuntime(r.Context(), settings)
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
	loginState := oidcStateToken{
		State:     state,
		Nonce:     nonce,
		ReturnTo:  sanitizeReturnTo(r.URL.Query().Get("return_to")),
		ExpiresAt: now.Add(oidcStateTTL),
		IssuedAt:  now,
	}
	if s.desktopMode {
		capability, ok := s.requireDesktopBrowserCapability(w, r)
		if !ok {
			return
		}
		if !s.storeDesktopOIDCState(capability, loginState) {
			writeError(w, http.StatusServiceUnavailable, "desktop authentication state capacity is unavailable")
			return
		}
	} else {
		encodedState, err := encodeSignedCookieValue(loginState, runtime.sessionSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to initialize oidc state")
			return
		}
		s.setSignedAuthCookie(w, settings, &http.Cookie{
			Name:     oidcStateCookieName,
			Value:    encodedState,
			Path:     "/api/v1/auth",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  now.Add(oidcStateTTL),
			MaxAge:   int(oidcStateTTL.Seconds()),
		})
	}
	writeAuthNavigation(w, r, runtime.oauth2Config.AuthCodeURL(state, oidc.Nonce(nonce)))
}

// writeAuthNavigation supports capability-bearing fetches from desktop browser
// tabs without weakening the top-level navigation boundary. The initiating
// request returns the provider URL as JSON after state was stored in the
// appropriate hosted-cookie or desktop-capability channel. Ordinary hosted
// links retain the traditional redirect response.
func writeAuthNavigation(w http.ResponseWriter, r *http.Request, redirectURL string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
		writeJSON(w, http.StatusOK, map[string]string{"redirect_url": redirectURL})
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	settings := s.authSettingsForRequest()
	if settings.Mode != authModeOIDC {
		writeError(w, http.StatusNotFound, "oidc callback is not enabled")
		return
	}
	if s.desktopMode {
		if r.Method == http.MethodGet {
			writeDesktopOAuthCallbackLanding(w, "Completing APEX Load sign-in")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "desktop oidc callback requires POST finalization")
			return
		}
	} else if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "oidc callback requires GET")
		return
	}
	var err error
	settings, err = s.effectiveOIDCSettings(settings)
	if err != nil {
		s.recordOIDCCallbackFailure(r, "desktop_callback_unavailable", map[string]any{"error": err.Error()})
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	runtime, err := s.cachedOIDCRuntime(r.Context(), settings)
	if err != nil {
		s.recordOIDCCallbackFailure(r, "runtime_initialization_failed", map[string]any{"error": err.Error()})
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	var loginState oidcStateToken
	var desktopCapability string
	if s.desktopMode {
		var ok bool
		desktopCapability, ok = s.requireDesktopBrowserCapability(w, r)
		if !ok {
			s.recordOIDCCallbackFailure(r, "missing_desktop_capability", nil)
			return
		}
		loginState, ok = s.takeDesktopOIDCState(desktopCapability, subtleQueryValue(r.URL.Query().Get("state")))
		if !ok {
			s.recordOIDCCallbackFailure(r, "invalid_or_expired_desktop_state", nil)
			writeError(w, http.StatusUnauthorized, "authentication state is invalid, expired, or belongs to another browser tab")
			return
		}
	} else {
		stateCookie, cookieErr := r.Cookie(oidcStateCookieName)
		if cookieErr != nil {
			s.recordOIDCCallbackFailure(r, "missing_state_cookie", nil)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if err := decodeSignedCookieValue(stateCookie.Value, runtime.sessionSecret, &loginState); err != nil || loginState.State == "" || time.Now().UTC().After(loginState.ExpiresAt) {
			s.recordOIDCCallbackFailure(r, "invalid_or_expired_state_cookie", nil)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
	}
	if subtleQueryValue(r.URL.Query().Get("state")) != loginState.State {
		s.recordOIDCCallbackFailure(r, "state_mismatch", nil)
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		s.recordOIDCCallbackFailure(r, "missing_authorization_code", nil)
		writeError(w, http.StatusBadRequest, "authorization code is required")
		return
	}
	token, err := runtime.exchange(r.Context(), code)
	if err != nil {
		s.recordOIDCCallbackFailure(r, "token_exchange_failed", nil)
		writeError(w, http.StatusUnauthorized, "oidc token exchange failed")
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		s.recordOIDCCallbackFailure(r, "missing_id_token", nil)
		writeError(w, http.StatusUnauthorized, "oidc id_token is missing")
		return
	}
	idToken, err := runtime.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		s.recordOIDCCallbackFailure(r, "id_token_verification_failed", nil)
		writeError(w, http.StatusUnauthorized, "oidc id_token verification failed")
		return
	}
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		s.recordOIDCCallbackFailure(r, "id_token_claims_invalid", nil)
		writeError(w, http.StatusUnauthorized, "oidc id_token claims are invalid")
		return
	}
	if strings.TrimSpace(stringClaim(claims["nonce"])) != loginState.Nonce {
		s.recordOIDCCallbackFailure(r, "nonce_mismatch", nil)
		writeError(w, http.StatusUnauthorized, "oidc nonce verification failed")
		return
	}
	user, session, err := authenticatedUserFromOIDCClaims(claims, idToken.Expiry, settings)
	if err != nil {
		s.recordOIDCCallbackFailure(r, "claim_mapping_failed", map[string]any{"error": err.Error()})
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	// Bind the cookie to the immutable local user id as well as the current
	// session epoch. A subject can be renamed and later reused by a different
	// principal; subject-only cookies would otherwise transfer to that new user.
	if s.store != nil {
		if local, lookupErr := s.store.GetUserBySubject(user.Subject); lookupErr == nil && local != nil {
			session.UserID = strings.TrimSpace(local.ID)
			session.SessionEpoch = local.SessionEpoch
			user.ID = session.UserID
			user.SessionEpoch = local.SessionEpoch
		} else if resolved, resolveErr := applyLocalUserBindings(user, s.store); resolveErr == nil && resolved != nil {
			// The production first-admin path provisions on first authentication.
			// Doing that before signing the cookie lets the initial session carry
			// the same immutable binding as every later login. Dev/no-store flows
			// intentionally continue to operate without a persisted local id.
			session.UserID = strings.TrimSpace(resolved.ID)
			session.SessionEpoch = resolved.SessionEpoch
			user = resolved
		}
	}
	encodedSession, err := encodeSignedCookieValue(session, runtime.sessionSecret)
	if err != nil {
		s.recordOIDCCallbackFailure(r, "session_creation_failed", map[string]any{"error": err.Error()})
		writeError(w, http.StatusInternalServerError, "failed to create oidc session")
		return
	}
	if s.desktopMode {
		if !s.bindDesktopOIDCSession(desktopCapability, loginState.State, encodedSession) {
			s.recordOIDCCallbackFailure(r, "desktop_capability_expired", nil)
			writeError(w, http.StatusUnauthorized, "desktop browser capability expired before the session could be established")
			return
		}
	} else {
		s.setSignedAuthCookie(w, settings, &http.Cookie{
			Name:     oidcSessionCookieName,
			Value:    encodedSession,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  session.ExpiresAt,
			MaxAge:   maxInt(1, int(time.Until(session.ExpiresAt).Seconds())),
		})
		s.clearAuthCookie(w, settings, oidcStateCookieName, "/api/v1/auth")
	}
	s.recordAuditEvent(withAuthenticatedContext(r, authResolution{User: user}), "auth.oidc.login", "success", "auth_session", user.Subject, "", nil)
	if s.desktopMode {
		writeJSON(w, http.StatusOK, map[string]string{"redirect_url": loginState.ReturnTo})
		return
	}
	http.Redirect(w, r, loginState.ReturnTo, http.StatusSeeOther)
}

func (s *Server) recordOIDCCallbackFailure(r *http.Request, reason string, details map[string]any) {
	safeDetails := map[string]any{"reason": reason}
	for key, value := range details {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if normalizedKey == "" || strings.Contains(normalizedKey, "token") || strings.Contains(normalizedKey, "code") || strings.Contains(normalizedKey, "secret") {
			continue
		}
		safeDetails[normalizedKey] = value
	}
	s.recordAuditEvent(r, "auth.oidc.callback", "failure", "auth_session", "", "", safeDetails)
}

func (s *Server) handleOIDCLogout(w http.ResponseWriter, r *http.Request) {
	settings := s.authSettingsForRequest()
	if settings.Mode != authModeOIDC {
		writeError(w, http.StatusNotFound, "oidc logout is not enabled")
		return
	}
	user, err := authenticatedUserFromOIDCSession(r, settings)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err := validateOIDCLogoutCSRFToken(r, user, settings); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	s.recordAuditEvent(withAuthenticatedContext(r, authResolution{User: user}), "auth.oidc.logout", "success", "auth_session", user.Subject, "", nil)
	if s.desktopMode {
		s.clearDesktopOIDCSession(desktopBrowserCapabilityFromRequest(r))
	} else {
		s.clearAuthCookie(w, settings, oidcSessionCookieName, "/")
		s.clearAuthCookie(w, settings, oidcStateCookieName, "/api/v1/auth")
	}
	destination := settings.LogoutURL
	if strings.TrimSpace(destination) == "" {
		destination = defaultOIDCReturnTo
	}
	writeJSON(w, http.StatusOK, AuthLogoutResponse{RedirectURL: destination})
}

type oidcRuntime struct {
	oauth2Config  oauth2.Config
	verifier      *oidc.IDTokenVerifier
	sessionSecret string
	httpClient    *http.Client
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
	httpClient := newOIDCHTTPClient()
	providerCtx := context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	provider, err := newOIDCProvider(providerCtx, settings.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery failed: %w", err)
	}
	endpoint := provider.Endpoint()
	for label, raw := range map[string]string{
		"authorization endpoint": endpoint.AuthURL,
		"token endpoint":         endpoint.TokenURL,
	} {
		if err := validateOIDCSecureURL(label, raw); err != nil {
			return nil, err
		}
	}
	var metadata struct {
		JWKSURL string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("oidc discovery metadata is invalid: %w", err)
	}
	if err := validateOIDCSecureURL("jwks endpoint", metadata.JWKSURL); err != nil {
		return nil, err
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
		httpClient:    httpClient,
	}, nil
}

func (runtime *oidcRuntime) exchange(ctx context.Context, code string) (*oauth2.Token, error) {
	if runtime == nil || runtime.httpClient == nil {
		return nil, fmt.Errorf("oidc runtime is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, runtime.httpClient)
	return runtime.oauth2Config.Exchange(ctx, code)
}

type oidcBoundedRoundTripper struct {
	base http.RoundTripper
}

func (transport oidcBoundedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := transport.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.Body != nil {
		resp.Body = http.MaxBytesReader(nil, resp.Body, oidcRemoteResponseMaxBytes)
	}
	return resp, nil
}

func newOIDCHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = oidcResponseHeaderMaxBytes
	return &http.Client{
		Timeout:       defaultOIDCDiscoveryTimeout,
		Transport:     oidcBoundedRoundTripper{base: transport},
		CheckRedirect: sameOriginRedirectPolicy,
	}
}

func validateOIDCSecureURL(label, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("oidc %s must be a valid http(s) URL", label)
	}
	if parsed.User != nil {
		return fmt.Errorf("oidc %s must not contain userinfo credentials", label)
	}
	if parsed.Scheme == "https" || oidcURLIsLoopback(parsed) {
		return nil
	}
	return fmt.Errorf("oidc %s must use https (http is allowed only for loopback development)", label)
}

func oidcURLIsLoopback(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// desktopLoopbackCallbackURL builds an OAuth callback from the listener's
// actual random port. Native OAuth clients register a loopback redirect and
// the provider returns to the port selected for this launch, rather than the
// static placeholder kept in persisted settings.
func (s *Server) desktopLoopbackCallbackURL(callbackPath string) (string, error) {
	if s == nil || !s.desktopMode {
		return "", fmt.Errorf("desktop callback is unavailable")
	}
	if !s.BoundAddrReady() {
		return "", fmt.Errorf("desktop API server is not ready")
	}
	parsed, err := url.Parse(s.BrowserURL())
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || !oidcURLIsLoopback(parsed) {
		return "", fmt.Errorf("desktop API callback must use a loopback HTTP origin")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("desktop API callback port is unavailable")
	}
	if !strings.HasPrefix(callbackPath, "/") || strings.HasPrefix(callbackPath, "//") {
		return "", fmt.Errorf("desktop API callback path is invalid")
	}
	parsed.Path = callbackPath
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (s *Server) effectiveOIDCSettings(settings AuthSettings) (AuthSettings, error) {
	if s == nil || !s.desktopMode {
		return settings, nil
	}
	callbackURL, err := s.desktopLoopbackCallbackURL(oidcCallbackPath)
	if err != nil {
		return settings, err
	}
	settings.RedirectURL = callbackURL
	return settings, nil
}

func (s *Server) cachedOIDCRuntime(ctx context.Context, settings AuthSettings) (*oidcRuntime, error) {
	if s == nil {
		return nil, fmt.Errorf("oidc server is not configured")
	}
	key := oidcRuntimeConfigurationKey(settings)
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oidcRuntime != nil && s.oidcRuntimeKey == key {
		return s.oidcRuntime, nil
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, defaultOIDCDiscoveryTimeout)
	defer cancel()
	runtime, err := newOIDCRuntime(discoveryCtx, settings)
	if err != nil {
		return nil, err
	}
	s.oidcRuntime = runtime
	s.oidcRuntimeKey = key
	return runtime, nil
}

func oidcRuntimeConfigurationKey(settings AuthSettings) string {
	parts := []string{
		settings.IssuerURL,
		settings.ClientID,
		settings.RedirectURL,
		strings.Join(settings.Scopes, "\x00"),
		settings.ClientSecretEnvVar,
		settings.SessionSecretEnvVar,
		os.Getenv(settings.ClientSecretEnvVar),
		os.Getenv(settings.SessionSecretEnvVar),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x01")))
	return fmt.Sprintf("%x", sum[:])
}

func (s *Server) allowOIDCLoginAttempt(w http.ResponseWriter, r *http.Request) bool {
	if s == nil || s.cache == nil {
		return true
	}
	limit := defaultOIDCLoginRatePerMinute
	if raw := strings.TrimSpace(os.Getenv("APEXLOAD_OIDC_LOGIN_RATE_LIMIT_PER_MINUTE")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusServiceUnavailable, "oidc login rate limit is misconfigured")
			return false
		}
		limit = parsed
	}
	peer := "unknown"
	if ip := requestSourceIP(r); ip != nil {
		peer = ip.String()
	}
	allowed, remaining, err := s.cache.AllowRateLimit(r.Context(), "oidc-login:"+peer, limit, time.Minute)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "oidc login rate limit is unavailable")
		return false
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "oidc login rate limit exceeded")
		return false
	}
	return true
}

func newOIDCProvider(ctx context.Context, issuerURL string) (*oidc.Provider, error) {
	if err := validateOIDCSecureURL("issuer URL", issuerURL); err != nil {
		return nil, err
	}
	if _, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); !ok {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, newOIDCHTTPClient())
	}
	return oidc.NewProvider(ctx, issuerURL)
}

func authenticatedUserFromOIDCClaims(claims map[string]any, expiry time.Time, settings AuthSettings) (*AuthenticatedUser, oidcSessionToken, error) {
	subject := strings.TrimSpace(stringClaim(claims["sub"]))
	if subject == "" {
		return nil, oidcSessionToken{}, fmt.Errorf("oidc subject claim is required")
	}
	if err := validateExternalAuthSubject(subject); err != nil {
		return nil, oidcSessionToken{}, err
	}
	globalRoles := normalizeRoleList(anyToStringSlice(claims[settings.RolesClaim]))
	projectRoles := normalizeProjectRoleClaims(claims[settings.ProjectRolesClaim])
	domainRoles := normalizeScopedRoleClaims(claims[settings.DomainRolesClaim], normalizeDomainID)
	repositoryRoles := normalizeScopedRoleClaims(claims[settings.RepositoryRolesClaim], strings.TrimSpace)
	if expiry.IsZero() {
		expiry = time.Now().UTC().Add(oidcSessionTTL)
	}
	maxExpiry := time.Now().UTC().Add(oidcSessionTTL)
	if expiry.After(maxExpiry) {
		expiry = maxExpiry
	}
	session := oidcSessionToken{
		Subject:         subject,
		DisplayName:     strings.TrimSpace(stringClaim(claims["name"])),
		Email:           strings.TrimSpace(stringClaim(claims["email"])),
		GlobalRoles:     globalRoles,
		ProjectRoles:    projectRoles,
		DomainRoles:     domainRoles,
		RepositoryRoles: repositoryRoles,
		IssuedAt:        time.Now().UTC(),
		ExpiresAt:       expiry,
	}
	return &AuthenticatedUser{
		Subject:          session.Subject,
		DisplayName:      session.DisplayName,
		Email:            session.Email,
		GlobalRoles:      sliceToRoleSet(session.GlobalRoles),
		ProjectRoles:     mapToProjectRoleSet(session.ProjectRoles),
		DomainRoles:      mapToDomainRoleSet(session.DomainRoles),
		RepositoryRoles:  mapToRepositoryRoleSet(session.RepositoryRoles),
		SessionScoped:    true,
		SessionExpiresAt: session.ExpiresAt,
	}, session, nil
}

func authSessionSecret(settings AuthSettings) (string, error) {
	secret := strings.TrimSpace(os.Getenv(settings.SessionSecretEnvVar))
	if secret == "" {
		return "", fmt.Errorf("oidc session secret environment variable %q is not set", settings.SessionSecretEnvVar)
	}
	if productionMode() && len([]byte(secret)) < productionSecretMinBytes {
		return "", fmt.Errorf("oidc session secret environment variable %q must contain at least %d bytes in production", settings.SessionSecretEnvVar, productionSecretMinBytes)
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

func issueOIDCLogoutCSRFToken(user *AuthenticatedUser, settings AuthSettings) (string, error) {
	if user == nil || strings.TrimSpace(user.Subject) == "" {
		return "", fmt.Errorf("authentication required")
	}
	secret, err := authSessionSecret(settings)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	return encodeSignedCookieValue(oidcLogoutCSRFToken{
		Subject:   user.Subject,
		Action:    "logout",
		ExpiresAt: now.Add(oidcLogoutCSRFTTL),
		IssuedAt:  now,
	}, secret)
}

func validateOIDCLogoutCSRFToken(r *http.Request, user *AuthenticatedUser, settings AuthSettings) error {
	if user == nil || strings.TrimSpace(user.Subject) == "" {
		return fmt.Errorf("authentication required")
	}
	secret, err := authSessionSecret(settings)
	if err != nil {
		return err
	}
	tokenValue := strings.TrimSpace(r.Header.Get("X-APEXLoad-CSRF-Token"))
	if tokenValue == "" {
		return fmt.Errorf("missing csrf token")
	}
	var token oidcLogoutCSRFToken
	if err := decodeSignedCookieValue(tokenValue, secret, &token); err != nil {
		return fmt.Errorf("invalid csrf token")
	}
	if token.Action != "logout" || time.Now().UTC().After(token.ExpiresAt) || strings.TrimSpace(token.Subject) != user.Subject {
		return fmt.Errorf("invalid csrf token")
	}
	return nil
}

func (s *Server) setSignedAuthCookie(w http.ResponseWriter, settings AuthSettings, cookie *http.Cookie) {
	cookie.Secure = s.authCookieSecure(settings)
	http.SetCookie(w, cookie)
}

func (s *Server) clearAuthCookie(w http.ResponseWriter, settings AuthSettings, name string, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.authCookieSecure(settings),
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}

func (s *Server) authCookieSecure(settings AuthSettings) bool {
	return authCookieSecure(settings)
}

func authCookieSecure(settings AuthSettings) bool {
	// In a hardened production deployment, session cookies must always carry the
	// Secure attribute regardless of how the redirect URL is spelled.
	if productionMode() {
		return true
	}
	parsed, err := url.Parse(settings.RedirectURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https"
}

func sanitizeReturnTo(raw string) string {
	for _, character := range raw {
		if character < 0x20 || character == 0x7f || character == '\\' {
			return defaultOIDCReturnTo
		}
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultOIDCReturnTo
	}
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") {
		return defaultOIDCReturnTo
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.IsAbs() || parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" || parsed.User != nil {
		return defaultOIDCReturnTo
	}
	if !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(parsed.Path, "//") || strings.Contains(parsed.Path, "\\") {
		return defaultOIDCReturnTo
	}
	for _, character := range parsed.Path {
		if character < 0x20 || character == 0x7f {
			return defaultOIDCReturnTo
		}
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
	return mapToScopedRoleSet(projectRoles, normalizeProjectID)
}

func mapToDomainRoleSet(domainRoles map[string][]string) map[string]map[string]struct{} {
	return mapToScopedRoleSet(domainRoles, normalizeDomainID)
}

func mapToRepositoryRoleSet(repositoryRoles map[string][]string) map[string]map[string]struct{} {
	return mapToScopedRoleSet(repositoryRoles, strings.TrimSpace)
}

func mapToScopedRoleSet(scopedRoles map[string][]string, normalizeScope func(string) string) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{}, len(scopedRoles))
	for scopeID, roles := range scopedRoles {
		normalizedScopeID := normalizeScope(scopeID)
		if normalizedScopeID == "" {
			continue
		}
		roleSet := sliceToRoleSet(roles)
		if len(roleSet) == 0 {
			continue
		}
		out[normalizedScopeID] = roleSet
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
	return normalizeScopedRoleClaims(value, normalizeProjectID)
}

func normalizeScopedRoleClaims(value any, normalizeScope func(string) string) map[string][]string {
	out := make(map[string][]string)
	switch typed := value.(type) {
	case string:
		return projectRoleSetToList(parseScopedRolesHeader(typed, normalizeScope))
	case map[string]any:
		for scopeID, rawRoles := range typed {
			roles := normalizeRoleList(anyToStringSlice(rawRoles))
			if len(roles) > 0 {
				out[normalizeScope(scopeID)] = roles
			}
		}
	case map[string][]string:
		for scopeID, roles := range typed {
			normalized := normalizeRoleList(roles)
			if len(normalized) > 0 {
				out[normalizeScope(scopeID)] = normalized
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
