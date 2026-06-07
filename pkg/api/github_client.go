package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const githubAcceptHeader = "application/vnd.github+json"

// githubTLSConfig builds the TLS config for the GitHub transport, honoring an
// optional internal CA bundle and skip-verify (for self-hosted GHES with private
// PKI). Returns nil when no custom TLS material is configured (system trust).
func githubTLSConfig(settings GitHubSettings) (*tls.Config, error) {
	if !settings.TLSInsecureSkipVerify && strings.TrimSpace(settings.CACertFile) == "" {
		return nil, nil
	}
	cfg := &tls.Config{InsecureSkipVerify: settings.TLSInsecureSkipVerify} //nolint:gosec // operator-controlled GHES TLS knob
	if strings.TrimSpace(settings.CACertFile) != "" {
		pem, err := os.ReadFile(settings.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("github ca cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("github ca cert %q contained no valid certificates", settings.CACertFile)
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

func githubAllowSet(settings GitHubSettings) map[string]struct{} {
	hosts := settings.allowlistHosts()
	allow := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		allow[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}
	return allow
}

func newGitHubHTTPClient(settings GitHubSettings, timeout time.Duration) (*http.Client, error) {
	tlsCfg, err := githubTLSConfig(settings)
	if err != nil {
		return nil, err
	}
	return newHostAllowlistedHTTPClientWithTLS(settings.allowlistHosts(), tlsCfg, timeout), nil
}

// githubClient is a thin REST client for the GitHub (Enterprise) API, scoped to a
// single per-user token. All requests run through the host-allowlisted, optionally
// CA-pinned transport.
type githubClient struct {
	http    *http.Client
	apiBase string
	webBase string
	token   string
	allowed map[string]struct{}
}

func newGitHubClient(settings GitHubSettings, token string, timeout time.Duration) (*githubClient, error) {
	httpClient, err := newGitHubHTTPClient(settings, timeout)
	if err != nil {
		return nil, err
	}
	return &githubClient{
		http:    httpClient,
		apiBase: strings.TrimRight(settings.APIBaseURL, "/"),
		webBase: strings.TrimRight(settings.WebBaseURL, "/"),
		token:   token,
		allowed: githubAllowSet(settings),
	}, nil
}

// doJSON performs an authenticated API request and decodes a JSON response into
// out (may be nil). Non-2xx responses return a redacted error.
func (c *githubClient) doJSON(ctx context.Context, method, apiPath string, query url.Values, body any, out any) error {
	endpoint := c.apiBase + apiPath
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	if err := validateOutboundURLAllowingHosts(endpoint, c.allowed); err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", githubAcceptHeader)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github request to %s failed: %w", redactURL(endpoint), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &githubAPIError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(snippet)), URL: redactURL(endpoint)}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type githubAPIError struct {
	StatusCode int
	Message    string
	URL        string
}

func (e *githubAPIError) Error() string {
	return fmt.Sprintf("github api %s returned %d: %s", e.URL, e.StatusCode, e.Message)
}

type githubIdentity struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	AvatarURL string `json:"avatar_url"`
}

func (c *githubClient) Identity(ctx context.Context) (*githubIdentity, error) {
	var id githubIdentity
	if err := c.doJSON(ctx, http.MethodGet, "/user", nil, nil, &id); err != nil {
		return nil, err
	}
	return &id, nil
}

type githubTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// exchangeGitHubOAuthCode trades an authorization code for an access token at the
// configured (GHES) web base URL. Uses the host-allowlisted client; the client
// secret is read from the configured env var and never logged.
func exchangeGitHubOAuthCode(ctx context.Context, settings GitHubSettings, code, redirectURI string) (*githubTokenResponse, error) {
	endpoint := settings.WebBaseURL + "/login/oauth/access_token"
	if err := validateOutboundURLAllowingHosts(endpoint, githubAllowSet(settings)); err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("client_id", settings.ClientID)
	form.Set("client_secret", settings.clientSecret())
	form.Set("code", code)
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	httpClient, err := newGitHubHTTPClient(settings, 15*time.Second)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	var tokenResp githubTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("github token exchange returned an unreadable response")
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("github token exchange error: %s", tokenResp.Error)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, fmt.Errorf("github token exchange did not return an access token")
	}
	return &tokenResp, nil
}
