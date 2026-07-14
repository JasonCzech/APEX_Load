package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	oauthFormContentType        = "application/x-www-form-urlencoded"
	oauthJSONAcceptMimeType     = "application/json"
)

type OAuthTokenRequest struct {
	TokenURL     string
	GrantType    string
	ClientID     string
	ClientSecret string
	Scope        string
	Audience     string
	Issuer       string
	Subject      string
	PrivateKey   string
	RefreshToken string
	Username     string
	Password     string
	HTTPClient   *http.Client `json:"-"`
}

type OAuthToken struct {
	AccessToken string
	TokenType   string
	InstanceURL string
	ExpiresAt   time.Time
}

var (
	oauthTokenCache sync.Map
	oauthTokenGroup singleflight.Group
)

func FetchOAuth2Token(ctx context.Context, req OAuthTokenRequest) (OAuthToken, error) {
	req.GrantType = normalizeOAuthGrant(req.GrantType)
	if err := validateOAuthTokenRequest(req); err != nil {
		return OAuthToken{}, err
	}
	key := oauthTokenCacheKey(req)
	if cached, ok := oauthTokenCache.Load(key); ok {
		token := cached.(OAuthToken)
		if token.AccessToken != "" && time.Until(token.ExpiresAt) > tokenCacheExpirySlack {
			return token, nil
		}
	}
	value, err, _ := oauthTokenGroup.Do(key, func() (any, error) {
		if cached, ok := oauthTokenCache.Load(key); ok {
			token := cached.(OAuthToken)
			if token.AccessToken != "" && time.Until(token.ExpiresAt) > tokenCacheExpirySlack {
				return token, nil
			}
			oauthTokenCache.Delete(key)
		}
		token, fetchErr := fetchOAuth2TokenUncached(ctx, req)
		if fetchErr != nil {
			return OAuthToken{}, fetchErr
		}
		oauthTokenCache.Store(key, token)
		boundOAuthTokenCache()
		return token, nil
	})
	if err != nil {
		return OAuthToken{}, err
	}
	return value.(OAuthToken), nil
}

func boundOAuthTokenCache() {
	count := 0
	bytes := 0
	now := time.Now()
	oauthTokenCache.Range(func(key, value any) bool {
		token, ok := value.(OAuthToken)
		if !ok || token.ExpiresAt.Before(now) {
			oauthTokenCache.Delete(key)
			return true
		}
		count++
		bytes += oauthTokenApproxBytes(token)
		if count > maxOAuthTokenCacheEntries || bytes > maxOAuthTokenCacheBytes {
			oauthTokenCache.Delete(key)
		}
		return true
	})
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
	tokenURL, err := url.Parse(strings.TrimSpace(req.TokenURL))
	if err != nil || tokenURL.Scheme == "" || tokenURL.Host == "" {
		return fmt.Errorf("token_url must be an absolute URL")
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthToken{}, err
	}
	httpReq.Header.Set("Content-Type", oauthFormContentType)
	httpReq.Header.Set("Accept", oauthJSONAcceptMimeType)

	client := req.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()

	var payload struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		ExpiresIn        any    `json:"expires_in"`
		InstanceURL      string `json:"instance_url"`
		ErrorCode        string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOAuthTokenResponseBytes)).Decode(&payload); err != nil {
		return OAuthToken{}, fmt.Errorf("decode OAuth2 token response: %w", err)
	}
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
	if strings.TrimSpace(payload.AccessToken) == "" {
		return OAuthToken{}, fmt.Errorf("OAuth2 token response did not include access_token")
	}
	tokenType := strings.TrimSpace(payload.TokenType)
	if tokenType == "" {
		tokenType = defaultOAuthTokenType
	}
	ttl := defaultTokenCacheTTL
	if expiresIn := oauthExpiresInSeconds(payload.ExpiresIn); expiresIn > 0 {
		ttl = time.Duration(expiresIn) * time.Second
	}
	return OAuthToken{
		AccessToken: strings.TrimSpace(payload.AccessToken),
		TokenType:   tokenType,
		InstanceURL: strings.TrimSpace(payload.InstanceURL),
		ExpiresAt:   time.Now().Add(ttl),
	}, nil
}

func oauthExpiresInSeconds(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
			return int64(parsed)
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int64(parsed)
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
	for _, secret := range []string{req.ClientSecret, req.PrivateKey, req.RefreshToken, req.Password} {
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
		strings.TrimSpace(req.TokenURL),
		strings.TrimSpace(req.ClientID),
		strings.TrimSpace(req.Scope),
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
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}
