package http

import (
	"container/list"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
)

const (
	OAuthGrantClientCredentials = "client_credentials"
	OAuthGrantJWTBearer         = "jwt_bearer"
	OAuthGrantRefreshToken      = "refresh_token"
	OAuthGrantPassword          = "password"

	jwtBearerGrantURN     = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	defaultTokenCacheTTL  = 5 * time.Minute
	tokenCacheExpirySlack = 30 * time.Second
	// jwtBearerAssertionTTL is the validity window of the signed JWT bearer
	// assertion. It must comfortably cover network latency and clock skew between
	// this host and the token endpoint; 3 minutes proved too tight under
	// distributed/clock-skewed conditions, so the window is 5 minutes.
	jwtBearerAssertionTTL = 5 * time.Minute
	// maxOAuthErrorDescriptionLen bounds how much of an error endpoint's
	// error_description is echoed into a returned error message.
	maxOAuthErrorDescriptionLen = 256
	defaultOAuthTokenType       = "Bearer"
	maxOAuthTokenCacheEntries   = 10_000
	maxOAuthTokenCacheBytes     = 64 << 20
	maxOAuthTokenResponseBytes  = 1 << 20
	maxOAuthAccessTokenBytes    = 64 << 10
	maxOAuthTokenTypeBytes      = 256
	maxOAuthInstanceURLBytes    = 8 << 10
	maxOAuthRequestFieldBytes   = 64 << 10
	maxOAuthPrivateKeyBytes     = 1 << 20
	maxOAuthTokenTTL            = 24 * time.Hour
	maxConcurrentOAuthFetches   = 64
	defaultOAuthHTTPTimeout     = 30 * time.Second
	maxOAuthResponseHeaderBytes = 64 << 10
	oauthFormContentType        = "application/x-www-form-urlencoded"
	oauthJSONAcceptMimeType     = "application/json"
)

type OAuthTokenRequest struct {
	ClientAuthentication string
	TokenURL             string
	GrantType            string
	ClientID             string
	ClientSecret         string
	Scope                string
	Audience             string
	Issuer               string
	Subject              string
	PrivateKey           string
	RefreshToken         string
	Username             string
	Password             string
	HTTPClient           *http.Client `json:"-"`
}

type OAuthToken struct {
	AccessToken string
	TokenType   string
	InstanceURL string
	ExpiresAt   time.Time
}

var (
	oauthTokenCache        = newBoundedOAuthTokenCache()
	oauthTokenGroup        singleflight.Group
	oauthTokenFetchSlots   = make(chan struct{}, maxConcurrentOAuthFetches)
	defaultOAuthHTTPClient = hardenedOAuthHTTPClient(&http.Client{
		Transport: http.DefaultTransport,
		Timeout:   defaultOAuthHTTPTimeout,
	})
)

type oauthTokenCacheEntry struct {
	key   string
	token OAuthToken
	bytes int
}

type boundedOAuthTokenCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	order      *list.List
	bytes      int
	maxEntries int
	maxBytes   int
}

func newBoundedOAuthTokenCache() *boundedOAuthTokenCache {
	return newBoundedOAuthTokenCacheWithLimits(maxOAuthTokenCacheEntries, maxOAuthTokenCacheBytes)

}

func newBoundedOAuthTokenCacheWithLimits(maxEntries, maxBytes int) *boundedOAuthTokenCache {
	return &boundedOAuthTokenCache{
		entries:    make(map[string]*list.Element),
		order:      list.New(),
		maxEntries: max(1, maxEntries),
		maxBytes:   max(1, maxBytes),
	}
}

// hardenedOAuthHTTPClient preserves intentional network routing (source IP,
// proxy, timeouts) while separating credential acquisition from ordinary load
// traffic. Token requests must never inherit an insecure target TLS policy,
// target cookies, or redirects that can replay a 307/308 form body to another
// origin.
func hardenedOAuthHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		return nil
	}
	client := oauthRequestClient(base)
	if transport, ok := base.Transport.(*http.Transport); ok && transport != nil {
		secureTransport := transport.Clone()
		if secureTransport.TLSClientConfig == nil {
			secureTransport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			secureTransport.TLSClientConfig = secureTransport.TLSClientConfig.Clone()
			secureTransport.TLSClientConfig.InsecureSkipVerify = false
			if secureTransport.TLSClientConfig.MinVersion == 0 {
				secureTransport.TLSClientConfig.MinVersion = tls.VersionTLS12
			}
		}
		secureTransport.MaxResponseHeaderBytes = maxOAuthResponseHeaderBytes
		client.Transport = secureTransport
	}
	return client
}

func oauthRequestClient(base *http.Client) *http.Client {
	if base == nil {
		return nil
	}
	client := *base
	client.Jar = nil
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func FetchOAuth2Token(ctx context.Context, req OAuthTokenRequest) (OAuthToken, error) {
	if err := ctx.Err(); err != nil {
		return OAuthToken{}, err
	}
	req.GrantType = normalizeOAuthGrant(req.GrantType)
	if req.ClientAuthentication == "" {
		req.ClientAuthentication = "client_secret_post"
	}
	if err := validateOAuthTokenRequest(req); err != nil {
		return OAuthToken{}, err
	}
	key := oauthTokenCacheKey(req)
	if token, ok := loadOAuthToken(key); ok {
		return token, nil
	}
	pending := oauthTokenGroup.DoChan(key, func() (any, error) {
		if token, ok := loadOAuthToken(key); ok {
			return token, nil
		}
		if err := acquireOAuthTokenFetchSlot(ctx, oauthTokenFetchSlots); err != nil {
			return OAuthToken{}, err
		}
		defer releaseOAuthTokenFetchSlot(oauthTokenFetchSlots)
		token, fetchErr := fetchOAuth2TokenUncached(ctx, req)
		if fetchErr != nil {
			return OAuthToken{}, fetchErr
		}
		storeOAuthToken(key, token)
		return token, nil
	})
	select {
	case <-ctx.Done():
		return OAuthToken{}, ctx.Err()
	case result := <-pending:
		if result.Err != nil {
			return OAuthToken{}, result.Err
		}
		return result.Val.(OAuthToken), nil
	}
}

func loadOAuthToken(key string) (OAuthToken, bool) {
	if oauthTokenCache == nil {
		return OAuthToken{}, false
	}
	return oauthTokenCache.load(key, time.Now())
}

func storeOAuthToken(key string, token OAuthToken) {
	if oauthTokenCache != nil {
		oauthTokenCache.store(key, token)
	}
}

func boundOAuthTokenCache() {
	if oauthTokenCache != nil {
		oauthTokenCache.bound()
	}
}

func (c *boundedOAuthTokenCache) load(key string, now time.Time) (OAuthToken, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return OAuthToken{}, false
	}
	entry := element.Value.(*oauthTokenCacheEntry)
	if entry.token.AccessToken == "" || entry.token.ExpiresAt.Sub(now) <= tokenCacheExpirySlack {
		c.remove(element)
		return OAuthToken{}, false
	}
	c.order.MoveToBack(element)
	return entry.token, true
}

func (c *boundedOAuthTokenCache) store(key string, token OAuthToken) {
	if strings.TrimSpace(key) == "" {
		return
	}
	size := oauthTokenApproxBytes(token) + len(key)
	if size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing := c.entries[key]; existing != nil {
		c.remove(existing)
	}
	entry := &oauthTokenCacheEntry{key: key, token: token, bytes: size}
	element := c.order.PushBack(entry)
	c.entries[key] = element
	c.bytes += size
	c.boundLocked()
}

func (c *boundedOAuthTokenCache) bound() {
	c.mu.Lock()
	c.boundLocked()
	c.mu.Unlock()
}

func (c *boundedOAuthTokenCache) boundLocked() {
	for len(c.entries) > c.maxEntries || c.bytes > c.maxBytes {
		oldest := c.order.Front()
		if oldest == nil {
			break
		}
		c.remove(oldest)
	}
}

func (c *boundedOAuthTokenCache) remove(element *list.Element) {
	if element == nil {
		return
	}
	entry, _ := element.Value.(*oauthTokenCacheEntry)
	if entry != nil {
		delete(c.entries, entry.key)
		c.bytes -= entry.bytes
		if c.bytes < 0 {
			c.bytes = 0
		}
	}
	c.order.Remove(element)
}

func acquireOAuthTokenFetchSlot(ctx context.Context, slots chan struct{}) error {
	if slots == nil {
		return fmt.Errorf("OAuth2 token acquisition is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseOAuthTokenFetchSlot(slots chan struct{}) {
	if slots == nil {
		return
	}
	select {
	case <-slots:
	default:
	}
}

func oauthTokenApproxBytes(token OAuthToken) int {
	return len(token.AccessToken) + len(token.TokenType) + len(token.InstanceURL) + 64
}

func normalizeOAuthGrant(grant string) string {
	switch strings.ToLower(strings.TrimSpace(grant)) {
	case "", OAuthGrantClientCredentials:
		return OAuthGrantClientCredentials
	case OAuthGrantJWTBearer, "jwt", jwtBearerGrantURN:
		return OAuthGrantJWTBearer
	case OAuthGrantRefreshToken, "refresh":
		return OAuthGrantRefreshToken
	case OAuthGrantPassword:
		return OAuthGrantPassword
	default:
		return strings.ToLower(strings.TrimSpace(grant))
	}
}

func validateOAuthTokenRequest(req OAuthTokenRequest) error {
	if req.ClientAuthentication != "" && req.ClientAuthentication != "client_secret_post" && req.ClientAuthentication != "client_secret_basic" && req.ClientAuthentication != "kafka_3_3_1_basic" {
		return fmt.Errorf("unsupported OAuth client authentication method")
	}
	if (req.ClientAuthentication == "client_secret_basic" || req.ClientAuthentication == "kafka_3_3_1_basic") && req.GrantType != OAuthGrantClientCredentials {
		return fmt.Errorf("client_secret_basic requires client_credentials grant")
	}
	rawTokenURL := strings.TrimSpace(req.TokenURL)
	if len(rawTokenURL) > maxOAuthInstanceURLBytes {
		return fmt.Errorf("token_url must not exceed %d bytes", maxOAuthInstanceURLBytes)
	}
	tokenURL, err := url.Parse(rawTokenURL)
	if err != nil || tokenURL.Scheme == "" || tokenURL.Host == "" {
		return fmt.Errorf("token_url must be an absolute URL")
	}
	switch strings.ToLower(tokenURL.Scheme) {
	case "https":
	case "http":
		if !isOAuthLoopbackHost(tokenURL.Hostname()) {
			return fmt.Errorf("token_url must use https (http is allowed only for loopback development endpoints)")
		}
	default:
		return fmt.Errorf("token_url must use https")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "client_id", value: req.ClientID},
		{name: "client_secret", value: req.ClientSecret},
		{name: "scope", value: req.Scope},
		{name: "audience", value: req.Audience},
		{name: "issuer", value: req.Issuer},
		{name: "subject", value: req.Subject},
		{name: "refresh_token", value: req.RefreshToken},
		{name: "username", value: req.Username},
		{name: "password", value: req.Password},
	} {
		if len(field.value) > maxOAuthRequestFieldBytes {
			return fmt.Errorf("%s must not exceed %d bytes", field.name, maxOAuthRequestFieldBytes)
		}
	}
	if len(req.PrivateKey) > maxOAuthPrivateKeyBytes {
		return fmt.Errorf("private_key must not exceed %d bytes", maxOAuthPrivateKeyBytes)
	}
	if strings.TrimSpace(req.ClientID) == "" {
		return fmt.Errorf("client_id is required")
	}
	switch req.GrantType {
	case OAuthGrantClientCredentials:
		if strings.TrimSpace(req.ClientSecret) == "" {
			return fmt.Errorf("client_secret is required for client_credentials")
		}
	case OAuthGrantJWTBearer:
		if strings.TrimSpace(req.Subject) == "" {
			return fmt.Errorf("subject is required for jwt_bearer")
		}
		if strings.TrimSpace(req.PrivateKey) == "" {
			return fmt.Errorf("private_key is required for jwt_bearer")
		}
	case OAuthGrantRefreshToken:
		if strings.TrimSpace(req.RefreshToken) == "" {
			return fmt.Errorf("refresh_token is required for refresh_token grant")
		}
	case OAuthGrantPassword:
		if strings.TrimSpace(req.Username) == "" {
			return fmt.Errorf("username is required for password grant")
		}
		if strings.TrimSpace(req.Password) == "" {
			return fmt.Errorf("password is required for password grant")
		}
	default:
		return fmt.Errorf("unsupported OAuth2 grant_type %q", req.GrantType)
	}
	return nil
}

func isOAuthLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func fetchOAuth2TokenUncached(ctx context.Context, req OAuthTokenRequest) (OAuthToken, error) {
	form := url.Values{}
	switch req.GrantType {
	case OAuthGrantClientCredentials:
		form.Set("grant_type", OAuthGrantClientCredentials)
		form.Set("client_id", req.ClientID)
		form.Set("client_secret", req.ClientSecret)
		if strings.TrimSpace(req.Scope) != "" {
			form.Set("scope", strings.TrimSpace(req.Scope))
		}
	case OAuthGrantJWTBearer:
		assertion, err := buildJWTBearerAssertion(req, time.Now())
		if err != nil {
			return OAuthToken{}, err
		}
		form.Set("grant_type", jwtBearerGrantURN)
		form.Set("assertion", assertion)
	case OAuthGrantRefreshToken:
		form.Set("grant_type", OAuthGrantRefreshToken)
		form.Set("client_id", req.ClientID)
		form.Set("refresh_token", req.RefreshToken)
		if strings.TrimSpace(req.ClientSecret) != "" {
			form.Set("client_secret", req.ClientSecret)
		}
		if strings.TrimSpace(req.Scope) != "" {
			form.Set("scope", strings.TrimSpace(req.Scope))
		}
	case OAuthGrantPassword:
		form.Set("grant_type", OAuthGrantPassword)
		form.Set("client_id", req.ClientID)
		form.Set("username", req.Username)
		form.Set("password", req.Password)
		if strings.TrimSpace(req.ClientSecret) != "" {
			form.Set("client_secret", req.ClientSecret)
		}
		if strings.TrimSpace(req.Scope) != "" {
			form.Set("scope", strings.TrimSpace(req.Scope))
		}
	default:
		return OAuthToken{}, fmt.Errorf("unsupported OAuth2 grant_type %q", req.GrantType)
	}

	if req.ClientAuthentication == "client_secret_basic" || req.ClientAuthentication == "kafka_3_3_1_basic" {
		form.Del("client_id")
		form.Del("client_secret")
	}
	encodedForm := form.Encode()
	if req.ClientAuthentication == "kafka_3_3_1_basic" {
		encodedForm = "grant_type=client_credentials"
		if scope := trimKafka331Value(req.Scope); scope != "" {
			encodedForm += "&scope=" + strings.ReplaceAll(strings.ReplaceAll(url.QueryEscape(scope), "%2A", "*"), "~", "%7E")
		}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.TokenURL, strings.NewReader(encodedForm))
	if err != nil {
		return OAuthToken{}, err
	}
	if req.ClientAuthentication == "client_secret_basic" {
		httpReq.SetBasicAuth(url.QueryEscape(req.ClientID), url.QueryEscape(req.ClientSecret))
	} else if req.ClientAuthentication == "kafka_3_3_1_basic" {
		httpReq.Header.Set("Authorization", "Basic "+base64.URLEncoding.EncodeToString([]byte(trimKafka331Value(req.ClientID)+":"+trimKafka331Value(req.ClientSecret))))
	}
	httpReq.Header.Set("Content-Type", oauthFormContentType)
	httpReq.Header.Set("Accept", oauthJSONAcceptMimeType)

	client := req.HTTPClient
	if client == nil {
		client = defaultOAuthHTTPClient
	}
	client = oauthRequestClient(client)
	resp, err := client.Do(httpReq)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthTokenResponseBytes+1))
	if err != nil {
		return OAuthToken{}, fmt.Errorf("read OAuth2 token response: %w", err)
	}
	if len(responseBody) > maxOAuthTokenResponseBytes {
		return OAuthToken{}, fmt.Errorf("OAuth2 token response exceeds %d bytes", maxOAuthTokenResponseBytes)
	}
	var payload struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        any    `json:"expires_in"`
		InstanceURL      string `json:"instance_url"`
		ErrorCode        string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	decodeErr := json.Unmarshal(responseBody, &payload)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// error_code is the RFC 6749 §5.2 enum (safe). error_description is
		// free-form text from the endpoint: bound its length and strip any of our
		// own request secrets it may echo back before surfacing it in an error
		// that gets logged/stored in result.ErrorMessage.
		desc := redactOAuthErrorDescription(payload.ErrorDescription, req)
		if payload.ErrorCode != "" || desc != "" {
			return OAuthToken{}, fmt.Errorf("OAuth2 token endpoint returned %d: %s %s", resp.StatusCode, payload.ErrorCode, desc)
		}
		return OAuthToken{}, fmt.Errorf("OAuth2 token endpoint returned %d", resp.StatusCode)
	}
	if decodeErr != nil {
		return OAuthToken{}, fmt.Errorf("decode OAuth2 token response: %w", decodeErr)
	}
	accessToken := strings.TrimSpace(payload.AccessToken)
	if accessToken == "" {
		return OAuthToken{}, fmt.Errorf("OAuth2 token response did not include access_token")
	}
	if len(accessToken) > maxOAuthAccessTokenBytes {
		return OAuthToken{}, fmt.Errorf("OAuth2 access_token exceeds %d bytes", maxOAuthAccessTokenBytes)
	}
	if strings.ContainsAny(accessToken, "\r\n\x00") {
		return OAuthToken{}, fmt.Errorf("OAuth2 access_token contains prohibited control characters")
	}
	tokenType := strings.TrimSpace(payload.TokenType)
	if tokenType == "" {
		tokenType = defaultOAuthTokenType
	}
	if len(tokenType) > maxOAuthTokenTypeBytes || strings.ContainsAny(tokenType, "\r\n\x00") {
		return OAuthToken{}, fmt.Errorf("OAuth2 token_type is invalid or exceeds %d bytes", maxOAuthTokenTypeBytes)
	}
	instanceURL := strings.TrimSpace(payload.InstanceURL)
	if len(instanceURL) > maxOAuthInstanceURLBytes || strings.ContainsAny(instanceURL, "\r\n\x00") {
		return OAuthToken{}, fmt.Errorf("OAuth2 instance_url is invalid or exceeds %d bytes", maxOAuthInstanceURLBytes)
	}
	ttl := defaultTokenCacheTTL
	if expiresIn := oauthExpiresInSeconds(payload.ExpiresIn); expiresIn > 0 {
		if expiresIn > int64(maxOAuthTokenTTL/time.Second) {
			expiresIn = int64(maxOAuthTokenTTL / time.Second)
		}
		ttl = time.Duration(expiresIn) * time.Second
	}
	return OAuthToken{
		AccessToken: accessToken,
		TokenType:   tokenType,
		InstanceURL: instanceURL,
		ExpiresAt:   time.Now().Add(ttl),
	}, nil
}

func oauthExpiresInSeconds(value any) int64 {
	fromFloat := func(seconds float64) int64 {
		if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
			return 0
		}
		maxSeconds := float64(maxOAuthTokenTTL / time.Second)
		if seconds > maxSeconds {
			return int64(maxSeconds)
		}
		return int64(seconds)
	}
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return fromFloat(typed)
	case float32:
		return fromFloat(float64(typed))
	case int:
		if typed <= 0 {
			return 0
		}
		return min(int64(typed), int64(maxOAuthTokenTTL/time.Second))
	case int64:
		if typed <= 0 {
			return 0
		}
		return min(typed, int64(maxOAuthTokenTTL/time.Second))
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return oauthExpiresInSeconds(parsed)
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return fromFloat(parsed)
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return oauthExpiresInSeconds(parsed)
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return fromFloat(parsed)
		}
	}
	return 0
}

// redactOAuthErrorDescription bounds the length of an OAuth endpoint's
// error_description and replaces any occurrence of the request's own secrets
// (client_secret, refresh_token, private_key, password) with a placeholder so a
// misbehaving or malicious endpoint cannot echo our credentials back into a
// logged error string.
func redactOAuthErrorDescription(desc string, req OAuthTokenRequest) string {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return ""
	}
	for _, secret := range []string{base64.URLEncoding.EncodeToString([]byte(trimKafka331Value(req.ClientID) + ":" + trimKafka331Value(req.ClientSecret))), base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(req.ClientID) + ":" + url.QueryEscape(req.ClientSecret))), req.ClientSecret, url.QueryEscape(req.ClientSecret), req.PrivateKey, req.RefreshToken, req.Password} {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			desc = strings.ReplaceAll(desc, secret, "[redacted]")
		}
	}
	if len(desc) > maxOAuthErrorDescriptionLen {
		desc = desc[:maxOAuthErrorDescriptionLen] + "…"
	}
	return desc
}

func buildJWTBearerAssertion(req OAuthTokenRequest, now time.Time) (string, error) {
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(req.PrivateKey))
	if err != nil {
		return "", fmt.Errorf("parse jwt_bearer private_key: %w", err)
	}
	issuer := strings.TrimSpace(req.Issuer)
	if issuer == "" {
		issuer = strings.TrimSpace(req.ClientID)
	}
	audience := strings.TrimSpace(req.Audience)
	if audience == "" {
		tokenURL, _ := url.Parse(req.TokenURL)
		audience = tokenURL.Scheme + "://" + tokenURL.Host
	}
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": strings.TrimSpace(req.Subject),
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(jwtBearerAssertionTTL).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

func oauthTokenCacheKey(req OAuthTokenRequest) string {
	parts := []string{
		req.GrantType,
		req.ClientAuthentication,
		strings.TrimSpace(req.TokenURL),
		req.ClientID,
		req.Scope,
		strings.TrimSpace(req.Audience),
		strings.TrimSpace(req.Issuer),
		strings.TrimSpace(req.Subject),
		secretFingerprint(req.RefreshToken),
		strings.TrimSpace(req.Username),
		secretFingerprint(req.ClientSecret),
		secretFingerprint(req.PrivateKey),
		secretFingerprint(req.Password),
	}
	// Keep the sync.Map key bounded even when data-driven client fields are
	// unexpectedly large. The digest also avoids retaining those raw strings in
	// the long-lived cache.
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func secretFingerprint(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Java String.trim removes only code units through U+0020.
func trimKafka331Value(value string) string {
	return strings.TrimFunc(value, func(r rune) bool { return r <= 0x20 })
}
