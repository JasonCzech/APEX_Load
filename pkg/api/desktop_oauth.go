package api

import (
	"crypto/subtle"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

const maxDesktopOAuthStates = 64

type desktopBrowserCapability struct {
	issuedAt          time.Time
	oidcSessionCookie string
	localSessionToken string
	oidcFlowState     string
	devViewMode       string
	projectID         string
}

type desktopOIDCStateGrant struct {
	capability string
	state      oidcStateToken
}

type desktopGitHubStateGrant struct {
	capability string
	state      githubStateToken
}

func desktopBrowserCapabilityFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	capability := strings.TrimSpace(r.Header.Get(desktopCapabilityHeader))
	if capability == "" && strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		capability = strings.TrimSpace(r.URL.Query().Get(desktopCapabilityQuery))
	}
	return capability
}

func (s *Server) requireDesktopBrowserCapability(w http.ResponseWriter, r *http.Request) (string, bool) {
	capability := desktopBrowserCapabilityFromRequest(r)
	if capability == "" || !s.validDesktopBrowserCapability(capability) {
		writeError(w, http.StatusUnauthorized, "desktop browser capability is missing or invalid; reopen APEX Load in the system browser")
		return "", false
	}
	return capability, true
}

func (s *Server) storeDesktopOIDCState(capability string, state oidcStateToken) bool {
	if s == nil || capability == "" || state.State == "" {
		return false
	}
	now := time.Now().UTC()
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	capabilityGrant, ok := s.desktopBrowserCapabilities[capability]
	if !ok {
		return false
	}
	for key, grant := range s.desktopOIDCStates {
		// A capability is one tab-local authentication channel. A newer login
		// supersedes any older pending state for that channel so a duplicated tab
		// cannot complete an older IdP flow after a newer account was selected.
		if !grant.state.ExpiresAt.After(now) || subtle.ConstantTimeCompare([]byte(grant.capability), []byte(capability)) == 1 {
			delete(s.desktopOIDCStates, key)
		}
	}
	if len(s.desktopOIDCStates) >= maxDesktopOAuthStates {
		return false
	}
	capabilityGrant.oidcFlowState = state.State
	s.desktopBrowserCapabilities[capability] = capabilityGrant
	s.desktopOIDCStates[state.State] = desktopOIDCStateGrant{capability: capability, state: state}
	return true
}

func (s *Server) takeDesktopOIDCState(capability, stateValue string) (oidcStateToken, bool) {
	if s == nil || capability == "" || stateValue == "" {
		return oidcStateToken{}, false
	}
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	grant, ok := s.desktopOIDCStates[stateValue]
	if !ok || !grant.state.ExpiresAt.After(time.Now().UTC()) {
		if ok {
			delete(s.desktopOIDCStates, stateValue)
		}
		return oidcStateToken{}, false
	}
	if subtle.ConstantTimeCompare([]byte(grant.capability), []byte(capability)) != 1 {
		return oidcStateToken{}, false
	}
	capabilityGrant, capabilityOK := s.desktopBrowserCapabilities[capability]
	if !capabilityOK || subtle.ConstantTimeCompare([]byte(capabilityGrant.oidcFlowState), []byte(stateValue)) != 1 {
		delete(s.desktopOIDCStates, stateValue)
		return oidcStateToken{}, false
	}
	delete(s.desktopOIDCStates, stateValue)
	return grant.state, true
}

// storeDesktopGitHubLoginState atomically snapshots the initiating development
// preview and installs the single pending GitHub OAuth state for a browser
// capability. Keeping both mutations under desktopBootstrapMu prevents two
// duplicated tabs from interleaving preview A with state B.
func (s *Server) storeDesktopGitHubLoginState(capability string, r *http.Request, state githubStateToken) (bool, error) {
	if s == nil || capability == "" || r == nil || state.State == "" {
		return false, nil
	}
	var preview *DesktopDevPreviewContext
	settings := s.authSettingsForRequest()
	if !productionMode() && settings.Mode == authModeDev {
		parsed, err := ParseDesktopDevPreviewContext(r.Header.Get(devViewHeader), r.Header.Get(devProjectIDHeader))
		if err != nil {
			return false, err
		}
		expectedUserID := "local"
		if parsed.Mode() == devViewModeUser {
			expectedUserID = "dev-user"
		}
		if strings.TrimSpace(state.UserID) != expectedUserID {
			return false, fmt.Errorf("desktop github authentication identity does not match the development preview")
		}
		preview = &parsed
	}
	now := time.Now().UTC()
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	capabilityGrant, ok := s.desktopBrowserCapabilities[capability]
	if !ok {
		return false, nil
	}
	for key, grant := range s.desktopGitHubStates {
		// A browser capability represents one tab-local authentication channel.
		// Starting a newer GitHub flow supersedes its older pending flow so a
		// later Dev View switch cannot mutate the identity used by two concurrent
		// callbacks. Duplicated tabs that copied sessionStorage follow the same
		// deterministic newest-flow-wins rule.
		if !grant.state.ExpiresAt.After(now) || subtle.ConstantTimeCompare([]byte(grant.capability), []byte(capability)) == 1 {
			delete(s.desktopGitHubStates, key)
		}
	}
	if len(s.desktopGitHubStates) >= maxDesktopOAuthStates {
		return false, nil
	}
	if preview != nil {
		capabilityGrant.devViewMode = preview.Mode()
		capabilityGrant.projectID = preview.ProjectID()
		s.desktopBrowserCapabilities[capability] = capabilityGrant
	}
	s.desktopGitHubStates[state.State] = desktopGitHubStateGrant{capability: capability, state: state}
	return true, nil
}

func (s *Server) takeDesktopGitHubState(capability, stateValue string) (githubStateToken, bool) {
	if s == nil || capability == "" || stateValue == "" {
		return githubStateToken{}, false
	}
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	grant, ok := s.desktopGitHubStates[stateValue]
	if !ok || !grant.state.ExpiresAt.After(time.Now().UTC()) {
		if ok {
			delete(s.desktopGitHubStates, stateValue)
		}
		return githubStateToken{}, false
	}
	if subtle.ConstantTimeCompare([]byte(grant.capability), []byte(capability)) != 1 {
		return githubStateToken{}, false
	}
	delete(s.desktopGitHubStates, stateValue)
	return grant.state, true
}

func (s *Server) bindDesktopOIDCSession(capability, flowState, encodedSession string) bool {
	if s == nil || capability == "" || flowState == "" || encodedSession == "" {
		return false
	}
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	grant, ok := s.desktopBrowserCapabilities[capability]
	if !ok || subtle.ConstantTimeCompare([]byte(grant.oidcFlowState), []byte(flowState)) != 1 {
		return false
	}
	grant.oidcSessionCookie = encodedSession
	grant.oidcFlowState = ""
	s.desktopBrowserCapabilities[capability] = grant
	return true
}

func (s *Server) desktopOIDCSessionForCapability(capability string) (string, bool) {
	if s == nil || capability == "" {
		return "", false
	}
	s.desktopBootstrapMu.Lock()
	grant, ok := s.desktopBrowserCapabilities[capability]
	s.desktopBootstrapMu.Unlock()
	if !ok || grant.oidcSessionCookie == "" {
		return "", false
	}
	return grant.oidcSessionCookie, true
}

func (s *Server) desktopDevPreviewForCapability(capability string) (DesktopDevPreviewContext, bool) {
	if s == nil || capability == "" {
		return DesktopDevPreviewContext{}, false
	}
	s.desktopBootstrapMu.Lock()
	grant, ok := s.desktopBrowserCapabilities[capability]
	s.desktopBootstrapMu.Unlock()
	if !ok || grant.devViewMode == "" {
		return DesktopDevPreviewContext{}, false
	}
	preview, err := ParseDesktopDevPreviewContext(grant.devViewMode, grant.projectID)
	return preview, err == nil
}

func (s *Server) clearDesktopOIDCSession(capability string) {
	if s == nil || capability == "" {
		return
	}
	s.desktopBootstrapMu.Lock()
	if grant, ok := s.desktopBrowserCapabilities[capability]; ok {
		grant.oidcSessionCookie = ""
		grant.oidcFlowState = ""
		s.desktopBrowserCapabilities[capability] = grant
	}
	// Logout is a revocation boundary. A pending callback from the same tab (or
	// a duplicated tab that copied sessionStorage) must not re-authenticate the
	// capability after the user explicitly signed out.
	for state, pending := range s.desktopOIDCStates {
		if subtle.ConstantTimeCompare([]byte(pending.capability), []byte(capability)) == 1 {
			delete(s.desktopOIDCStates, state)
		}
	}
	s.desktopBootstrapMu.Unlock()
}

func (s *Server) bindDesktopLocalSession(capability, token string) bool {
	if s == nil || capability == "" || token == "" {
		return false
	}
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	if subtle.ConstantTimeCompare([]byte(capability), []byte(s.desktopCapability)) == 1 {
		s.desktopLocalSessionToken = token
		return true
	}
	grant, ok := s.desktopBrowserCapabilities[capability]
	if !ok {
		return false
	}
	grant.localSessionToken = token
	s.desktopBrowserCapabilities[capability] = grant
	return true
}

func (s *Server) desktopLocalSessionForCapability(capability string) (string, bool) {
	if s == nil || capability == "" {
		return "", false
	}
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	if subtle.ConstantTimeCompare([]byte(capability), []byte(s.desktopCapability)) == 1 {
		return s.desktopLocalSessionToken, s.desktopLocalSessionToken != ""
	}
	grant, ok := s.desktopBrowserCapabilities[capability]
	return grant.localSessionToken, ok && grant.localSessionToken != ""
}

func (s *Server) clearDesktopLocalSession(capability string) {
	if s == nil || capability == "" {
		return
	}
	s.desktopBootstrapMu.Lock()
	defer s.desktopBootstrapMu.Unlock()
	if subtle.ConstantTimeCompare([]byte(capability), []byte(s.desktopCapability)) == 1 {
		s.desktopLocalSessionToken = ""
		return
	}
	if grant, ok := s.desktopBrowserCapabilities[capability]; ok {
		grant.localSessionToken = ""
		s.desktopBrowserCapabilities[capability] = grant
	}
}

func (s *Server) clearDesktopGitHubState(capability string) {
	if s == nil || capability == "" {
		return
	}
	s.desktopBootstrapMu.Lock()
	for state, grant := range s.desktopGitHubStates {
		if subtle.ConstantTimeCompare([]byte(grant.capability), []byte(capability)) == 1 {
			delete(s.desktopGitHubStates, state)
		}
	}
	s.desktopBootstrapMu.Unlock()
}

// desktopAuthMaterialMiddleware rejects host-scoped loopback cookies as an
// authentication channel. After the outer capability middleware has accepted a
// port-scoped browser grant, it injects the corresponding signed OIDC session
// only into the in-process request passed to the normal auth implementation.
func desktopAuthMaterialMiddleware(
	resolveSession func(string) (string, bool),
	resolveLocalSession func(string) (string, bool),
	resolvePreviews ...func(string) (DesktopDevPreviewContext, bool),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			protected := strings.HasPrefix(r.URL.Path, "/api/v1/") || r.URL.Path == "/metrics"
			if !protected {
				next.ServeHTTP(w, r)
				return
			}
			cookies := r.Cookies()
			r.Header.Del("Cookie")
			for _, cookie := range cookies {
				switch cookie.Name {
				case oidcSessionCookieName, localSessionCookieName, oidcStateCookieName, githubStateCookieName:
					continue
				default:
					r.AddCookie(cookie)
				}
			}
			capability := desktopBrowserCapabilityFromRequest(r)
			if encodedSession, ok := resolveSession(capability); ok {
				r.AddCookie(&http.Cookie{Name: oidcSessionCookieName, Value: encodedSession})
			}
			if resolveLocalSession != nil {
				if token, ok := resolveLocalSession(capability); ok {
					r.AddCookie(&http.Cookie{Name: localSessionCookieName, Value: token})
				}
			}
			// OAuth callback landing pages can attach only their sessionStorage
			// capability. If no explicit preview header is present, restore the
			// validated context bound to that capability at bootstrap so callback
			// identity checks run as the same dev user/project that initiated them.
			if strings.TrimSpace(r.Header.Get(devViewHeader)) == "" {
				for _, resolvePreview := range resolvePreviews {
					if resolvePreview == nil {
						continue
					}
					if preview, ok := resolvePreview(capability); ok {
						r.Header.Del(devProjectIDHeader)
						preview.Apply(r.Header)
						break
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeDesktopOAuthCallbackLanding(w http.ResponseWriter, title string) {
	scriptNonce, err := generateDesktopSecret()
	if err != nil {
		http.Error(w, "desktop authentication callback failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+scriptNonce+"'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head><body><p id="status">Completing authentication…</p><script nonce="%s">const status=document.getElementById("status");const capability=sessionStorage.getItem("apexload.desktopCapability")||"";if(!capability){status.textContent="This authentication tab is no longer authorized. Reopen APEX Load from the desktop app and try again.";}else{fetch(location.pathname+location.search,{method:"POST",credentials:"omit",headers:{"Accept":"application/json","X-APEXLoad-Desktop-Token":capability}}).then(async response=>{const payload=await response.json().catch(()=>({}));if(!response.ok)throw new Error(payload.error||"Authentication failed");location.replace(payload.redirect_url||"/");}).catch(error=>{status.textContent=error instanceof Error?error.message:"Authentication failed";});}</script><noscript>JavaScript is required to complete authentication.</noscript></body></html>`, html.EscapeString(title), scriptNonce)
}

func expireLegacyDesktopOAuthCookies(w http.ResponseWriter) {
	for _, cookie := range []struct {
		name string
		path string
	}{
		{name: oidcSessionCookieName, path: "/"},
		{name: oidcStateCookieName, path: "/api/v1/auth"},
		{name: githubStateCookieName, path: "/api/v1/integrations/github"},
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     cookie.name,
			Value:    "",
			Path:     cookie.path,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
			Expires:  time.Unix(0, 0).UTC(),
		})
	}
}
