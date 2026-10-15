package browser

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	playwright "github.com/mxschmitt/playwright-go"
)

// A salesforce_jwt observer's browser context is signed in before its first
// step. The generator mints an access token for the observer's username with
// the OAuth 2.0 JWT bearer flow, asks Salesforce's single-access UI bridge for
// a one-time frontdoor URL, opens that URL in a throwaway context with no
// evidence capture, and seeds the observer's context with the cookies it
// leaves. The token, the frontdoor URL and its one-time password never reach
// the observer's evidence or an error message.

const (
	salesforceJWTLoginURLKey   = "browser_session_jwt_login_url"
	salesforceJWTClientIDKey   = "browser_session_jwt_client_id"
	salesforceJWTPrivateKeyKey = "browser_session_jwt_private_key"
	salesforceJWTAudienceKey   = "browser_session_jwt_audience"
	salesforceJWTStartURLKey   = "browser_session_jwt_start_url"

	// salesforceSingleAccessPath is the UI bridge endpoint on the token's
	// instance; salesforceSingleAccessRedirectParam names the path it lands on.
	salesforceSingleAccessPath          = "/services/oauth2/singleaccess"
	salesforceSingleAccessRedirectParam = "redirect_uri"
	salesforceJWTTokenPath              = "/services/oauth2/token"

	// salesforceJWTBridgeTimeout bounds a whole sign-in (token, frontdoor URL
	// and bridge page), within a session preflight's per-observer budget.
	salesforceJWTBridgeTimeout   = 30 * time.Second
	maxSingleAccessResponseBytes = 64 << 10
	maxSingleAccessErrorBytes    = 200
)

// salesforceJWTSession is one observer's salesforce_jwt sign-in.
type salesforceJWTSession struct {
	loginURL   string
	clientID   string
	privateKey string
	audience   string
	startURL   string
	username   string
	// allowedHosts is the script's target policy, which the token endpoint,
	// the instance and the frontdoor URL must all satisfy.
	allowedHosts []string
}

// salesforceBridgeHTTPClient asks for frontdoor URLs: no cookies, no
// redirects (a redirect could carry the bearer token elsewhere), TLS 1.2+.
var salesforceBridgeHTTPClient = func() *http.Client {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}()

// configureSalesforceJWTSession reads the observer's salesforce_jwt sign-in,
// if any, from its protocol config.
func (b *Browser) configureSalesforceJWTSession(config map[string]any) error {
	b.salesforceJWT = nil
	loginURL := strings.TrimSpace(protocol.ConfigString(config, salesforceJWTLoginURLKey))
	if loginURL == "" {
		return nil
	}
	session := &salesforceJWTSession{
		loginURL:     strings.TrimRight(loginURL, "/"),
		clientID:     strings.TrimSpace(protocol.ConfigString(config, salesforceJWTClientIDKey)),
		privateKey:   protocol.ConfigString(config, salesforceJWTPrivateKeyKey),
		audience:     strings.TrimSpace(protocol.ConfigString(config, salesforceJWTAudienceKey)),
		startURL:     protocol.ConfigString(config, salesforceJWTStartURLKey),
		username:     strings.TrimSpace(protocol.ConfigString(config, "browser_identity_username")),
		allowedHosts: append([]string(nil), b.allowedTargetHosts...),
	}
	switch {
	case session.username == "":
		return fmt.Errorf("salesforce_jwt session: the observer has no identity username")
	case session.clientID == "" || strings.Contains(session.clientID, "{{secret."):
		return fmt.Errorf("salesforce_jwt session: configure the project secret config.browser.session.jwt.client_id names")
	case strings.TrimSpace(session.privateKey) == "" || strings.Contains(session.privateKey, "{{secret."):
		return fmt.Errorf("salesforce_jwt session: configure the project secret config.browser.session.jwt.private_key names")
	case !bridgeEndpointURL(session.loginURL):
		return fmt.Errorf("salesforce_jwt session: login_url must be an https origin")
	case session.audience != "" && !bridgeEndpointURL(session.audience):
		return fmt.Errorf("salesforce_jwt session: audience must be an https origin")
	case !strings.HasPrefix(session.startURL, "/") || strings.HasPrefix(session.startURL, "//"):
		return fmt.Errorf("salesforce_jwt session: start_url must be a path starting with /")
	case !session.hostAllowed(session.loginURL):
		return fmt.Errorf("salesforce_jwt session: login_url is outside allowed target hosts")
	}
	b.salesforceJWT = session
	return nil
}

// hostAllowed reports a URL the script's target policy admits, or any URL
// without a policy.
func (s *salesforceJWTSession) hostAllowed(raw string) bool {
	return len(s.allowedHosts) == 0 || protocol.TargetURLAllowed(raw, s.allowedHosts)
}

// bridgeEndpointURL accepts the URLs the OAuth client accepts for its token
// endpoint, without credentials.
func bridgeEndpointURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.User == nil && httpprotocol.OAuthEndpointURLAllowed(raw)
}

// salesforceJWTStorageState signs the observer in through a throwaway
// context and returns the storage state its context starts with. The sign-in
// ends with the caller's context (Init's, or the iteration's), and within
// salesforceJWTBridgeTimeout in any case.
func (b *Browser) salesforceJWTStorageState(ctx context.Context, pwBrowser playwright.Browser) (*playwright.OptionalStorageState, error) {
	session := b.salesforceJWT
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, salesforceJWTBridgeTimeout)
	defer cancel()
	frontdoor, err := session.frontdoorURL(ctx)
	if err != nil {
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: %w", session.username, err)
	}
	options := b.browserContextOptions()
	options.StorageState = nil
	bridge, err := pwBrowser.NewContext(options)
	if err != nil {
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: open the bridge context: %w", session.username, err)
	}
	defer func() { _ = bridge.Close() }()
	// The bridge needs the session's cookies, not the page's look: skip the
	// heavy resources, so the extra context stays light while it lives.
	if err := bridge.Route("**/*", func(route playwright.Route) {
		switch route.Request().ResourceType() {
		case "image", "media", "font", "stylesheet":
			_ = route.Abort()
		default:
			_ = route.Continue()
		}
	}); err != nil {
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: restrict the bridge context: %w", session.username, err)
	}
	page, err := bridge.NewPage()
	if err != nil {
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: open the bridge page: %w", session.username, err)
	}
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline)
	if remaining < time.Second {
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: no time left to open the UI bridge", session.username)
	}
	if _, err := page.Goto(frontdoor, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(float64(remaining / time.Millisecond)),
	}); err != nil {
		// The error names the frontdoor URL and its one-time password: report
		// only its kind.
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: the UI bridge page did not load (%s)", session.username, navigationFailureKind(err))
	}
	if SalesforceSignInURL(page.URL()) {
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: the UI bridge sent the user to sign in", session.username)
	}
	state, err := bridge.StorageState()
	if err != nil {
		return nil, fmt.Errorf("salesforce_jwt sign-in for %s: read the bridged session: %w", session.username, err)
	}
	return state.ToOptionalStorageState(), nil
}

var navigationErrorCode = regexp.MustCompile(`net::ERR_[A-Z_]+`)

// navigationFailureKind names a navigation failure without its URL.
func navigationFailureKind(err error) string {
	text := err.Error()
	if code := navigationErrorCode.FindString(text); code != "" {
		return code
	}
	if strings.Contains(strings.ToLower(text), "timeout") {
		return fmt.Sprintf("timed out within the sign-in's %s", salesforceJWTBridgeTimeout)
	}
	return "navigation failed"
}

func (s *salesforceJWTSession) tokenRequest() httpprotocol.OAuthTokenRequest {
	return httpprotocol.OAuthTokenRequest{
		TokenURL:   s.loginURL + salesforceJWTTokenPath,
		GrantType:  httpprotocol.OAuthGrantJWTBearer,
		ClientID:   s.clientID,
		Subject:    s.username,
		PrivateKey: s.privateKey,
		Audience:   s.audience,
	}
}

// frontdoorURL mints (or reuses) the user's access token and exchanges it for
// a one-time frontdoor URL, minting once more when the bridge rejects a
// cached token.
func (s *salesforceJWTSession) frontdoorURL(ctx context.Context) (string, error) {
	request := s.tokenRequest()
	for attempt := 0; ; attempt++ {
		token, err := httpprotocol.FetchOAuth2Token(ctx, request)
		if err != nil {
			return "", fmt.Errorf("mint an access token: %w", err)
		}
		frontdoor, rejected, err := s.singleAccess(ctx, token)
		if rejected && attempt == 0 {
			httpprotocol.ForgetOAuth2Token(request)
			continue
		}
		return frontdoor, err
	}
}

// singleAccess asks the token's instance for a frontdoor URL, reporting
// whether it rejected the token itself.
func (s *salesforceJWTSession) singleAccess(ctx context.Context, token httpprotocol.OAuthToken) (string, bool, error) {
	instance := strings.TrimRight(token.InstanceURL, "/")
	if !bridgeEndpointURL(instance) {
		return "", false, fmt.Errorf("the token response named no usable instance_url")
	}
	if !s.hostAllowed(instance) {
		return "", false, fmt.Errorf("the token's instance is outside allowed target hosts")
	}
	form := url.Values{}
	form.Set(salesforceSingleAccessRedirectParam, s.startURL)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, instance+salesforceSingleAccessPath, strings.NewReader(form.Encode()))
	if err != nil {
		return "", false, fmt.Errorf("build the UI bridge request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := salesforceBridgeHTTPClient.Do(request)
	if err != nil {
		return "", false, fmt.Errorf("ask the UI bridge for a frontdoor URL: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSingleAccessResponseBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("read the UI bridge response: %w", err)
	}
	if len(body) > maxSingleAccessResponseBytes {
		return "", false, fmt.Errorf("the UI bridge response exceeds %d bytes", maxSingleAccessResponseBytes)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return "", true, fmt.Errorf("the UI bridge rejected the access token (401)")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", false, fmt.Errorf("the UI bridge returned %d%s", response.StatusCode, singleAccessErrorDetail(body, token.AccessToken))
	}
	var payload struct {
		FrontdoorURI string `json:"frontdoor_uri"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.FrontdoorURI == "" {
		return "", false, fmt.Errorf("the UI bridge response has no frontdoor_uri")
	}
	frontdoor, err := url.Parse(payload.FrontdoorURI)
	instanceURL, _ := url.Parse(instance)
	if err != nil || !bridgeEndpointURL(payload.FrontdoorURI) || !strings.EqualFold(frontdoor.Host, instanceURL.Host) {
		return "", false, fmt.Errorf("the UI bridge's frontdoor_uri is not on the token's instance")
	}
	return payload.FrontdoorURI, false, nil
}

// singleAccessErrorDetail is a refusal's error code and message, bounded,
// without the bearer token should the endpoint echo it.
func singleAccessErrorDetail(body []byte, accessToken string) string {
	var detail string
	var oauth struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	var rest []struct {
		ErrorCode string `json:"errorCode"`
		Message   string `json:"message"`
	}
	switch {
	case json.Unmarshal(body, &oauth) == nil && (oauth.Error != "" || oauth.Description != ""):
		detail = strings.TrimSpace(oauth.Error + " " + oauth.Description)
	case json.Unmarshal(body, &rest) == nil && len(rest) > 0:
		detail = strings.TrimSpace(rest[0].ErrorCode + " " + rest[0].Message)
	}
	if accessToken != "" {
		detail = strings.ReplaceAll(detail, accessToken, "[redacted]")
	}
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > maxSingleAccessErrorBytes {
		detail = strings.ToValidUTF8(detail[:maxSingleAccessErrorBytes], "") + "..."
	}
	if detail == "" {
		return ""
	}
	return ": " + detail
}
