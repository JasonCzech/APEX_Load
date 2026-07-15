package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
)

const (
	githubAcceptHeader          = "application/vnd.github+json"
	githubAllowInsecureHTTPEnv  = "APEXLOAD_GITHUB_ALLOW_INSECURE_HTTP"
	githubRemoteJSONMaxBytes    = 8 << 20
	githubOAuthResponseMaxBytes = 64 << 10
	githubOAuthTokenMaxBytes    = 16 << 10
	githubOAuthMetadataMaxBytes = 4 << 10
	githubOAuthMaxTokenTTL      = 7 * 24 * time.Hour
)

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
	for label, raw := range map[string]string{"github.api_base_url": settings.APIBaseURL, "github.web_base_url": settings.WebBaseURL} {
		if err := validateCredentialTransportURL(label, raw, githubAllowInsecureHTTPEnv); err != nil {
			return nil, err
		}
	}
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
		payload, err := io.ReadAll(io.LimitReader(resp.Body, githubRemoteJSONMaxBytes+1))
		if err != nil {
			return fmt.Errorf("read github response: %w", err)
		}
		if len(payload) > githubRemoteJSONMaxBytes {
			return fmt.Errorf("github response exceeded the %d-byte limit", githubRemoteJSONMaxBytes)
		}
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("decode github response: %w", err)
		}
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

type githubMeta struct {
	InstalledVersion                 string `json:"installed_version"`
	VerifiablePasswordAuthentication bool   `json:"verifiable_password_authentication"`
}

func (c *githubClient) Meta(ctx context.Context) (*githubMeta, error) {
	var meta githubMeta
	if err := c.doJSON(ctx, http.MethodGet, "/meta", nil, nil, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

type githubRepo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	HTMLURL       string `json:"html_url"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

type githubAppInstallation struct {
	ID                  int64  `json:"id"`
	RepositorySelection string `json:"repository_selection"`
	HTMLURL             string `json:"html_url"`
	AppID               int64  `json:"app_id"`
	AppSlug             string `json:"app_slug"`
	Account             struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
	Permissions map[string]string `json:"permissions"`
	Events      []string          `json:"events"`
	SuspendedAt *time.Time        `json:"suspended_at"`
}

func (c *githubClient) ListAppInstallations(ctx context.Context) ([]githubAppInstallation, error) {
	const perPage = 100
	const maxPages = 10
	all := make([]githubAppInstallation, 0, perPage)
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		q.Set("per_page", fmt.Sprint(perPage))
		q.Set("page", fmt.Sprint(page))
		var batch []githubAppInstallation
		if err := c.doJSON(ctx, http.MethodGet, "/app/installations", q, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break
		}
	}
	return all, nil
}

type githubInstallationToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *githubClient) CreateInstallationToken(ctx context.Context, installationID int64, repositories []string) (*githubInstallationToken, error) {
	apiPath := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	body := map[string]any{}
	if len(repositories) > 0 {
		body["repositories"] = repositories
	}
	var token githubInstallationToken
	if err := c.doJSON(ctx, http.MethodPost, apiPath, nil, body, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

// ListRepos returns repositories the connected user can access, most-recently
// updated first. It pages through results up to a bounded cap so a user with
// many repos still gets a usable, finite list for the push/pull pickers.
func (c *githubClient) ListRepos(ctx context.Context) ([]githubRepo, error) {
	const perPage = 100
	const maxPages = 10
	all := make([]githubRepo, 0, perPage)
	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		q.Set("per_page", fmt.Sprint(perPage))
		q.Set("page", fmt.Sprint(page))
		q.Set("sort", "updated")
		q.Set("affiliation", "owner,collaborator,organization_member")
		var batch []githubRepo
		if err := c.doJSON(ctx, http.MethodGet, "/user/repos", q, nil, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < perPage {
			break
		}
	}
	return all, nil
}

type githubBranch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// ListBranches returns the branches of a repository (bounded to a single large
// page, sufficient for the branch picker).
func (c *githubClient) ListBranches(ctx context.Context, owner, repo string) ([]githubBranch, error) {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("per_page", "100")
	var branches []githubBranch
	apiPath := fmt.Sprintf("/repos/%s/%s/branches", url.PathEscape(owner), url.PathEscape(repo))
	if err := c.doJSON(ctx, http.MethodGet, apiPath, q, nil, &branches); err != nil {
		return nil, err
	}
	return branches, nil
}

// githubContent is a single entry from the Contents API. For a file, Content
// holds the (base64) body and Encoding describes it; for a directory listing the
// API returns an array of these with Content empty.
type githubContent struct {
	Type     string `json:"type"` // "file" | "dir" | "symlink" | "submodule"
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
	Content  string `json:"content,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	HTMLURL  string `json:"html_url"`
}

// DecodedContent returns the decoded file body, validating the encoding.
func (f *githubContent) DecodedContent() ([]byte, error) {
	if f.Encoding != "" && f.Encoding != "base64" {
		return nil, fmt.Errorf("unsupported github content encoding %q", f.Encoding)
	}
	// GitHub wraps base64 content with newlines; StdEncoding rejects them.
	cleaned := strings.ReplaceAll(f.Content, "\n", "")
	return base64.StdEncoding.DecodeString(cleaned)
}

// githubContents is the result of a Contents API GET, which is either a single
// file (File set) or a directory listing (Entries set).
type githubContents struct {
	IsDir   bool
	File    *githubContent
	Entries []githubContent
}

// GetContents fetches a path in a repository at an optional ref (branch, tag, or
// sha). It transparently handles both the file and directory response shapes.
func (c *githubClient) GetContents(ctx context.Context, owner, repo, path, ref string) (*githubContents, error) {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return nil, err
	}
	if err := validateGitHubContentPath(path, true); err != nil {
		return nil, err
	}
	if ref != "" {
		if err := validateGitHubContentPath(ref, false); err != nil {
			return nil, fmt.Errorf("invalid github ref: %w", err)
		}
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), githubEscapeContentPath(path))
	q := url.Values{}
	if strings.TrimSpace(ref) != "" {
		q.Set("ref", ref)
	}
	var raw json.RawMessage
	if err := c.doJSON(ctx, http.MethodGet, apiPath, q, nil, &raw); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var entries []githubContent
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, err
		}
		return &githubContents{IsDir: true, Entries: entries}, nil
	}
	var file githubContent
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	return &githubContents{File: &file}, nil
}

type githubPutResult struct {
	Content struct {
		SHA     string `json:"sha"`
		Path    string `json:"path"`
		HTMLURL string `json:"html_url"`
	} `json:"content"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// PutFile creates or updates a file via the Contents API (a single direct
// commit on the given branch). sha must be the current blob sha when updating an
// existing file (GitHub optimistic concurrency); pass "" to create a new file.
func (c *githubClient) PutFile(ctx context.Context, owner, repo, path, branch, message string, content []byte, sha string) (*githubPutResult, error) {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return nil, err
	}
	if err := validateGitHubContentPath(path, false); err != nil {
		return nil, err
	}
	if branch != "" {
		if err := validateGitHubContentPath(branch, false); err != nil {
			return nil, fmt.Errorf("invalid github branch: %w", err)
		}
	}
	body := map[string]any{
		"message": message,
		"content": base64.StdEncoding.EncodeToString(content),
	}
	if strings.TrimSpace(branch) != "" {
		body["branch"] = branch
	}
	if strings.TrimSpace(sha) != "" {
		body["sha"] = sha
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/contents/%s", url.PathEscape(owner), url.PathEscape(repo), githubEscapeContentPath(path))
	var result githubPutResult
	if err := c.doJSON(ctx, http.MethodPut, apiPath, nil, body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetBranchHeadSHA returns the commit sha at the tip of a branch, or ("", nil)
// when the branch does not exist.
func (c *githubClient) GetBranchHeadSHA(ctx context.Context, owner, repo, branch string) (string, error) {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return "", err
	}
	if err := validateGitHubContentPath(branch, false); err != nil {
		return "", fmt.Errorf("invalid github branch: %w", err)
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", url.PathEscape(owner), url.PathEscape(repo), githubEscapeContentPath(branch))
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.doJSON(ctx, http.MethodGet, apiPath, nil, nil, &ref); err != nil {
		if apiErr, ok := err.(*githubAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
			return "", nil
		}
		return "", err
	}
	return ref.Object.SHA, nil
}

// CreateBranch creates a new branch ref pointing at fromSHA.
func (c *githubClient) CreateBranch(ctx context.Context, owner, repo, branch, fromSHA string) error {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return err
	}
	if err := validateGitHubContentPath(branch, false); err != nil {
		return fmt.Errorf("invalid github branch: %w", err)
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/git/refs", url.PathEscape(owner), url.PathEscape(repo))
	body := map[string]any{"ref": "refs/heads/" + branch, "sha": fromSHA}
	return c.doJSON(ctx, http.MethodPost, apiPath, nil, body, nil)
}

type githubPullResult struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
}

// CreatePull opens a pull request from head into base.
func (c *githubClient) CreatePull(ctx context.Context, owner, repo, title, head, base, body string) (*githubPullResult, error) {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return nil, err
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/pulls", url.PathEscape(owner), url.PathEscape(repo))
	payload := map[string]any{"title": title, "head": head, "base": base, "body": body}
	var result githubPullResult
	if err := c.doJSON(ctx, http.MethodPost, apiPath, nil, payload, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type githubCheckRun struct {
	ID         int64  `json:"id"`
	HTMLURL    string `json:"html_url"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func (c *githubClient) CreateCheckRun(ctx context.Context, owner, repo, name, headSHA, status, conclusion, summary, detailsURL string) (*githubCheckRun, error) {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return nil, err
	}
	body := map[string]any{
		"name":     name,
		"head_sha": headSHA,
		"status":   status,
		"output": map[string]any{
			"title":   name,
			"summary": summary,
		},
	}
	if conclusion != "" {
		body["conclusion"] = conclusion
	}
	if detailsURL != "" {
		body["details_url"] = detailsURL
	}
	apiPath := fmt.Sprintf("/repos/%s/%s/check-runs", url.PathEscape(owner), url.PathEscape(repo))
	var result githubCheckRun
	if err := c.doJSON(ctx, http.MethodPost, apiPath, nil, body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// githubEscapeContentPath escapes each path segment for use in a Contents API
// URL while preserving the "/" separators.
func githubEscapeContentPath(path string) string {
	segments := strings.Split(strings.TrimPrefix(strings.TrimSpace(path), "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func validateGitHubRepositoryCoordinates(owner, repo string) error {
	if err := validateGitHubResourceSegment("owner", owner); err != nil {
		return err
	}
	return validateGitHubResourceSegment("repository", repo)
}

func validateGitHubResourceSegment(label, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("github %s is required", label)
	}
	if value == "." || value == ".." || strings.ContainsAny(value, `/\\?#`) {
		return fmt.Errorf("github %s %q is not a safe path segment", label, value)
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return fmt.Errorf("github %s contains control characters", label)
		}
	}
	return nil
}

// validateGitHubContentPath prevents dot-segment and backslash traversal from
// escaping the intended Contents/refs API route after an intermediary performs
// URL normalization. Ordinary dotfiles (for example .github) remain valid.
func validateGitHubContentPath(value string, allowEmpty bool) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if allowEmpty {
			return nil
		}
		return fmt.Errorf("github path is required")
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") || strings.Contains(value, `\`) {
		return fmt.Errorf("github path %q is not a safe relative path", value)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("github path %q contains an unsafe segment", value)
		}
		for _, char := range segment {
			if char < 0x20 || char == 0x7f {
				return fmt.Errorf("github path contains control characters")
			}
		}
	}
	return nil
}

type githubTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func decodeGitHubOAuthTokenResponse(resp *http.Response, operation string) (*githubTokenResponse, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("%s returned an empty response", operation)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, githubOAuthResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s returned an unreadable response", operation)
	}
	if int64(len(payload)) > githubOAuthResponseMaxBytes {
		return nil, fmt.Errorf("%s response exceeded the %d-byte limit", operation, githubOAuthResponseMaxBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s returned HTTP status %d", operation, resp.StatusCode)
	}
	var tokenResp githubTokenResponse
	if err := json.Unmarshal(payload, &tokenResp); err != nil {
		return nil, fmt.Errorf("%s returned an unreadable response", operation)
	}
	for label, field := range map[string]struct {
		value string
		max   int
	}{
		"access token":      {value: tokenResp.AccessToken, max: githubOAuthTokenMaxBytes},
		"refresh token":     {value: tokenResp.RefreshToken, max: githubOAuthTokenMaxBytes},
		"token type":        {value: tokenResp.TokenType, max: githubOAuthMetadataMaxBytes},
		"scope":             {value: tokenResp.Scope, max: githubOAuthMetadataMaxBytes},
		"error":             {value: tokenResp.Error, max: githubOAuthMetadataMaxBytes},
		"error description": {value: tokenResp.ErrorDescription, max: githubOAuthMetadataMaxBytes},
	} {
		if err := validateGitHubOAuthResponseField(label, field.value, field.max); err != nil {
			return nil, err
		}
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("%s error: %s", operation, tokenResp.Error)
	}
	tokenResp.AccessToken = strings.TrimSpace(tokenResp.AccessToken)
	tokenResp.RefreshToken = strings.TrimSpace(tokenResp.RefreshToken)
	tokenResp.TokenType = strings.TrimSpace(tokenResp.TokenType)
	tokenResp.Scope = strings.TrimSpace(tokenResp.Scope)
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("%s did not return an access token", operation)
	}
	if _, err := githubOAuthExpiresAt(time.Now().UTC(), tokenResp.ExpiresIn); err != nil {
		return nil, fmt.Errorf("%s returned an invalid expiry: %w", operation, err)
	}
	return &tokenResp, nil
}

func validateGitHubOAuthResponseField(label, value string, maxBytes int) error {
	if len([]byte(value)) > maxBytes {
		return fmt.Errorf("github OAuth %s exceeded the %d-byte limit", label, maxBytes)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("github OAuth %s contains control characters", label)
		}
	}
	return nil
}

func githubOAuthExpiresAt(now time.Time, expiresIn int64) (*time.Time, error) {
	if expiresIn == 0 {
		return nil, nil
	}
	maxSeconds := int64(githubOAuthMaxTokenTTL / time.Second)
	if expiresIn < 0 || expiresIn > maxSeconds {
		return nil, fmt.Errorf("expires_in must be between 0 and %d seconds", maxSeconds)
	}
	expiresAt := now.UTC().Add(time.Duration(expiresIn) * time.Second)
	return &expiresAt, nil
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
	return decodeGitHubOAuthTokenResponse(resp, "github token exchange")
}
