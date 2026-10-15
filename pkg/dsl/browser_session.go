package dsl

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// BrowserSessionConfig gives each browser observer its own user and says how
// that user's session is signed in. The engine binds each observer, by its
// logical VUser ordinal, to one identity for its whole life; a run with more
// observers than identities fails before it starts.
type BrowserSessionConfig struct {
	// Strategy is storage_state_pool (each identity's storage state, exported
	// from the Recording Agent, seeds its browser context), salesforce_jwt
	// (each identity's browser is signed in with the OAuth 2.0 JWT bearer flow
	// and Salesforce's UI bridge, per jwt) or login_steps (the script signs in
	// with {{identity.username}} and {{identity.password}}).
	Strategy   string            `json:"strategy,omitempty"`
	Identities []BrowserIdentity `json:"identities,omitempty"`
	// PreflightURL is a light page (a path starting with /, which the base
	// URL prefixes, or an absolute http(s) URL) each storage_state_pool or
	// salesforce_jwt observer opens before the run starts; one sent to sign
	// in, or unable to sign in, fails the run, naming the identities whose
	// sessions are missing or expired.
	PreflightURL string `json:"preflight_url,omitempty"`
	// StartGapMs spaces the observers' starts, by logical ordinal across
	// all generators, to stay clear of the target's sign-in limits.
	StartGapMs int `json:"start_gap_ms,omitempty" jsonschema:"minimum=0,maximum=600000"`
	// JWT configures a salesforce_jwt session.
	JWT *BrowserSessionJWT `json:"jwt,omitempty"`
}

// BrowserSessionJWT signs each salesforce_jwt observer in as its identity's
// username: the generator mints an access token with the OAuth 2.0 JWT bearer
// flow (a connected app with a certificate, its users pre-authorized), asks
// Salesforce's single-access UI bridge for a one-time frontdoor URL, opens it
// in a throwaway browser context and seeds the observer's context with the
// resulting cookies. Neither the token nor the frontdoor URL reaches the
// observer's evidence.
type BrowserSessionJWT struct {
	// LoginURL is the token endpoint's origin: https://login.salesforce.com,
	// https://test.salesforce.com or the org's My Domain URL.
	LoginURL string `json:"login_url"`
	// ClientID is the connected app's consumer key, literal or a whole
	// {{secret.NAME}} reference.
	ClientID string `json:"client_id"`
	// PrivateKey is a whole {{secret.NAME}} reference to the PEM RSA private
	// key whose certificate the connected app holds.
	PrivateKey string `json:"private_key"`
	// Audience is the assertion's audience when it differs from LoginURL
	// (https://login.salesforce.com, or https://test.salesforce.com for a
	// sandbox, when LoginURL is a My Domain URL).
	Audience string `json:"audience,omitempty"`
	// StartURL is the path the UI bridge lands on, /lightning/page/home by
	// default.
	StartURL string `json:"start_url,omitempty"`
}

// BrowserIdentity is one observer's user. Its secrets are whole project-secret
// references, so they are resolved, sent to generators and redacted like any
// other secret the script names.
type BrowserIdentity struct {
	Username     string `json:"username"`
	StorageState string `json:"storage_state,omitempty"`
	Password     string `json:"password,omitempty"`
}

const (
	BrowserSessionStorageStatePool = "storage_state_pool"
	BrowserSessionSalesforceJWT    = "salesforce_jwt"
	BrowserSessionLoginSteps       = "login_steps"
	// MaxBrowserSessionIdentities bounds the identities a script lists.
	MaxBrowserSessionIdentities = 1000
	maxBrowserIdentityUsername  = 256
	maxBrowserSessionStartGapMs = 600000
)

// browserIdentityTemplate matches {{identity.username}} and
// {{identity.password}}, which each observer's copy of the script spells with
// its own identity.
var browserIdentityTemplate = regexp.MustCompile(`\{\{\s*identity\.([A-Za-z_]+)\s*\}\}`)

// BrowserSessionIdentities is the script's observer identities, or nil.
func BrowserSessionIdentities(script *Script) []BrowserIdentity {
	if script == nil || script.Config.Browser.Session == nil {
		return nil
	}
	return script.Config.Browser.Session.Identities
}

// BrowserSessionSignsInPerIteration reports whether the script's iteration
// signs in itself (its actions spell {{identity.*}}), so a sign-in page there
// is expected rather than a lost session.
func BrowserSessionSignsInPerIteration(script *Script) bool {
	if script == nil || len(script.Actions) == 0 {
		return false
	}
	encoded, err := json.Marshal(script.Actions)
	return err == nil && browserIdentityTemplate.Match(encoded)
}

// WithBrowserIdentity is the script as the observer with identity runs it:
// {{identity.username}} becomes the username and {{identity.password}} the
// identity's own {{secret.NAME}} reference, which the engine then resolves
// like any authored secret. The script is unchanged when it uses neither.
func (s *Script) WithBrowserIdentity(identity BrowserIdentity) (*Script, error) {
	if s == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("prepare the script for identity %q: %w", identity.Username, err)
	}
	if !browserIdentityTemplate.Match(encoded) {
		return s, nil
	}
	username, err := json.Marshal(identity.Username)
	if err != nil {
		return nil, err
	}
	password, err := json.Marshal(identity.Password)
	if err != nil {
		return nil, err
	}
	expanded := browserIdentityTemplate.ReplaceAllFunc(encoded, func(token []byte) []byte {
		switch string(browserIdentityTemplate.FindSubmatch(token)[1]) {
		case "username":
			return username[1 : len(username)-1]
		case "password":
			return password[1 : len(password)-1]
		}
		return token
	})
	var out Script
	if err := json.Unmarshal(expanded, &out); err != nil {
		return nil, fmt.Errorf("prepare the script for identity %q: %w", identity.Username, err)
	}
	return &out, nil
}

// BrowserSessionSeedsContext reports a strategy that signs each observer's
// browser context in before its first step (storage_state_pool,
// salesforce_jwt), rather than with the script's own steps.
func BrowserSessionSeedsContext(strategy string) bool {
	return strategy == BrowserSessionStorageStatePool || strategy == BrowserSessionSalesforceJWT
}

const (
	maxBrowserSessionJWTClientID  = 256
	defaultBrowserSessionJWTStart = "/lightning/page/home"
)

// BrowserSessionJWTStartURL is the path a salesforce_jwt session's UI bridge
// lands on.
func BrowserSessionJWTStartURL(jwt *BrowserSessionJWT) string {
	if jwt == nil || jwt.StartURL == "" {
		return defaultBrowserSessionJWTStart
	}
	return jwt.StartURL
}

func validateBrowserSessionJWTIssues(session *BrowserSessionConfig) []string {
	jwt := session.JWT
	if session.Strategy != BrowserSessionSalesforceJWT {
		if jwt != nil {
			return []string{"config.browser.session.jwt applies to a salesforce_jwt session"}
		}
		return nil
	}
	if jwt == nil {
		return []string{"config.browser.session.jwt is required by a salesforce_jwt session"}
	}
	var issues []string
	for _, origin := range [...]struct{ name, value string }{{"login_url", jwt.LoginURL}, {"audience", jwt.Audience}} {
		if origin.value == "" && origin.name == "audience" {
			continue
		}
		if !validBrowserSessionJWTOrigin(origin.value) {
			issues = append(issues, fmt.Sprintf("config.browser.session.jwt.%s must be an https origin such as https://login.salesforce.com", origin.name))
		}
	}
	clientID := jwt.ClientID
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientID) != clientID || len(clientID) > maxBrowserSessionJWTClientID || strings.ContainsAny(clientID, "\r\n\x00") ||
		(strings.Contains(clientID, "{{") && !browserStorageStateSecretPattern.MatchString(clientID)) {
		issues = append(issues, fmt.Sprintf("config.browser.session.jwt.client_id must be the consumer key (at most %d bytes) or a whole {{secret.NAME}} reference", maxBrowserSessionJWTClientID))
	}
	if !browserStorageStateSecretPattern.MatchString(jwt.PrivateKey) {
		issues = append(issues, "config.browser.session.jwt.private_key must be a whole {{secret.NAME}} reference")
	}
	if start := jwt.StartURL; start != "" && (!strings.HasPrefix(start, "/") || !validBrowserSessionPreflightURL(start)) {
		issues = append(issues, "config.browser.session.jwt.start_url must be a literal path starting with /, on one line and at most 2048 bytes")
	}
	return issues
}

// validBrowserSessionJWTOrigin accepts an https origin with no path, query
// or credentials.
func validBrowserSessionJWTOrigin(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && raw == strings.TrimSpace(raw) && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		(parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && parsed.Fragment == "" && !strings.Contains(raw, "{{")
}

// validBrowserSessionPreflightURL accepts a literal path, which navigation
// prefixes with the base URL, or an absolute http(s) URL.
func validBrowserSessionPreflightURL(preflight string) bool {
	if strings.TrimSpace(preflight) != preflight || len(preflight) > 2048 || strings.ContainsAny(preflight, "\r\n\x00") || strings.Contains(preflight, "{{") {
		return false
	}
	if strings.HasPrefix(preflight, "/") {
		return !strings.HasPrefix(preflight, "//")
	}
	parsed, err := url.Parse(preflight)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func validateBrowserSessionIssues(script *Script) []string {
	session := script.Config.Browser.Session
	encoded, _ := json.Marshal(script)
	uses := map[string]bool{}
	var issues []string
	for _, match := range browserIdentityTemplate.FindAllSubmatch(encoded, -1) {
		name := string(match[1])
		if !uses[name] && name != "username" && name != "password" {
			issues = append(issues, fmt.Sprintf("{{identity.%s}} is not an identity field; use {{identity.username}} or {{identity.password}}", name))
		}
		uses[name] = true
	}
	if session == nil {
		if len(uses) > 0 {
			issues = append(issues, "{{identity.*}} needs config.browser.session.identities")
		}
		return issues
	}
	if strings.TrimSpace(script.Protocol) != "browser" {
		return append(issues, "config.browser.session applies to browser scripts only")
	}
	switch session.Strategy {
	case BrowserSessionStorageStatePool, BrowserSessionSalesforceJWT, BrowserSessionLoginSteps:
	default:
		issues = append(issues, "config.browser.session.strategy must be storage_state_pool, salesforce_jwt or login_steps")
	}
	issues = append(issues, validateBrowserSessionJWTIssues(session)...)
	if len(session.Identities) == 0 || len(session.Identities) > MaxBrowserSessionIdentities {
		issues = append(issues, fmt.Sprintf("config.browser.session.identities must list 1 to %d identities", MaxBrowserSessionIdentities))
	}
	if session.StartGapMs < 0 || session.StartGapMs > maxBrowserSessionStartGapMs {
		issues = append(issues, fmt.Sprintf("config.browser.session.start_gap_ms must be 0 to %d", maxBrowserSessionStartGapMs))
	}
	if preflight := session.PreflightURL; preflight != "" {
		if !BrowserSessionSeedsContext(session.Strategy) {
			issues = append(issues, "config.browser.session.preflight_url applies to a storage_state_pool or salesforce_jwt session; a login_steps session signs in during init")
		} else if !validBrowserSessionPreflightURL(preflight) {
			issues = append(issues, "config.browser.session.preflight_url must be a literal path starting with / or an absolute http(s) URL, on one line and at most 2048 bytes")
		}
	}
	if BrowserSessionSeedsContext(session.Strategy) && script.Config.Browser.StorageState != "" {
		issues = append(issues, fmt.Sprintf("config.browser.storage_state and a %s session cannot both seed the browser", session.Strategy))
	}
	seen := map[string]bool{}
	for index, identity := range session.Identities {
		path := fmt.Sprintf("config.browser.session.identities[%d]", index)
		username := identity.Username
		switch {
		case strings.TrimSpace(username) == "" || strings.TrimSpace(username) != username:
			issues = append(issues, path+".username must be non-empty without surrounding whitespace")
		case len(username) > maxBrowserIdentityUsername || strings.ContainsAny(username, "\r\n\x00"):
			issues = append(issues, fmt.Sprintf("%s.username must be a single line of at most %d bytes", path, maxBrowserIdentityUsername))
		case seen[strings.ToLower(username)]:
			issues = append(issues, fmt.Sprintf("%s.username %q is listed twice; each observer needs its own user", path, username))
		}
		seen[strings.ToLower(username)] = true
		for _, field := range [...]struct{ name, value string }{{"storage_state", identity.StorageState}, {"password", identity.Password}} {
			if field.value != "" && !browserStorageStateSecretPattern.MatchString(field.value) {
				issues = append(issues, fmt.Sprintf("%s.%s must be a whole {{secret.NAME}} reference", path, field.name))
			}
		}
		if session.Strategy == BrowserSessionStorageStatePool && identity.StorageState == "" {
			issues = append(issues, path+".storage_state is required by a storage_state_pool session")
		}
		if session.Strategy != BrowserSessionStorageStatePool && identity.StorageState != "" {
			issues = append(issues, path+".storage_state applies to a storage_state_pool session")
		}
		if (session.Strategy == BrowserSessionLoginSteps || uses["password"]) && identity.Password == "" {
			issues = append(issues, path+".password is required where the script signs in with {{identity.password}}")
		}
	}
	return issues
}
