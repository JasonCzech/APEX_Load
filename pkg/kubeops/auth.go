package kubeops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AKSAADServerAppID is the well-known first-party application ID of the AKS
// AAD server component. A client-credentials token minted for this resource is
// what an AAD-integrated AKS API server accepts as a bearer token, which is why
// it is the default scope for azureServicePrincipalTokenSource.
const AKSAADServerAppID = "6dae42f8-4368-4678-94ff-3960e28e3630"

const (
	maxTokenEndpointResponseBytes int64 = 1 << 20
	// tokenRenewalSkew refreshes a cached bearer token before it actually
	// expires so an in-flight API call cannot race the expiry boundary.
	tokenRenewalSkew = 2 * time.Minute
)

// TokenSource produces the bearer token for Kubernetes API requests. The
// in-cluster service-account file, a long-lived static token, and an Azure AD
// service principal all satisfy it, so Client stays unaware of how a given
// cluster connection authenticates.
type TokenSource interface {
	// Token returns a currently-valid bearer token. Implementations that cache
	// are responsible for their own renewal.
	Token(ctx context.Context) (string, error)
	// Describe returns a non-secret, human-readable summary for diagnostics.
	Describe() string
}

// invalidatingTokenSource is an optional capability: a caching source
// implements it so a 401/403 drops the cached token instead of retrying the
// same rejected credential. Non-caching sources (file, static) do not need it.
type invalidatingTokenSource interface {
	Invalidate()
}

// ServiceAccountFileTokenSource reads the projected service-account token from
// disk on every call. Projected tokens rotate, so the value is deliberately not
// cached — this preserves the original in-cluster read-per-request behavior.
type ServiceAccountFileTokenSource struct {
	Path string
}

func (s ServiceAccountFileTokenSource) Token(context.Context) (string, error) {
	path := firstNonEmpty(s.Path, DefaultTokenFile)
	raw, err := readBoundedFile(path, maxServiceAccountTokenBytes)
	if err != nil {
		return "", fmt.Errorf("read Kubernetes service-account token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("Kubernetes service-account token is empty")
	}
	return token, nil
}

func (s ServiceAccountFileTokenSource) Describe() string {
	return "in-cluster service account (" + firstNonEmpty(s.Path, DefaultTokenFile) + ")"
}

// StaticTokenSource carries an operator-supplied, long-lived service-account
// token for a remote cluster. The token itself never appears in Describe.
type StaticTokenSource struct {
	Value string
}

func (s StaticTokenSource) Token(context.Context) (string, error) {
	token := strings.TrimSpace(s.Value)
	if token == "" {
		return "", fmt.Errorf("static Kubernetes token is empty")
	}
	return token, nil
}

func (StaticTokenSource) Describe() string { return "static service-account token" }

// allowedAzureAuthorityHosts bounds where a client secret may be POSTed. The
// authority is administrator-supplied, so without an allow-list a mistyped or
// malicious value would exfiltrate the credential to an arbitrary host. These
// are the Azure AD sovereign-cloud login endpoints.
var allowedAzureAuthorityHosts = map[string]struct{}{
	"login.microsoftonline.com":        {},
	"login.microsoftonline.us":         {},
	"login.partner.microsoftonline.cn": {},
}

// AzureServicePrincipalConfig configures the AAD client-credentials flow used
// to reach an AAD-integrated AKS cluster. Scope defaults to the AKS AAD server
// application; Authority defaults to the public Azure AD cloud.
type AzureServicePrincipalConfig struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	Scope        string
	Authority    string
	HTTPClient   *http.Client
}

// AzureServicePrincipalTokenSource mints AKS bearer tokens via the AAD
// client-credentials grant and caches them until shortly before expiry. It is
// safe for concurrent use.
type AzureServicePrincipalTokenSource struct {
	cfg AzureServicePrincipalConfig

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	// now is a test seam; nil means time.Now.
	now func() time.Time
}

// NewAzureServicePrincipalTokenSource validates the credential shape up front
// so a misconfigured cluster connection fails at save time rather than on the
// first scale attempt.
func NewAzureServicePrincipalTokenSource(cfg AzureServicePrincipalConfig) (*AzureServicePrincipalTokenSource, error) {
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.Scope = firstNonEmpty(cfg.Scope, AKSAADServerAppID+"/.default")
	cfg.Authority = strings.TrimRight(firstNonEmpty(cfg.Authority, "https://login.microsoftonline.com"), "/")
	if cfg.TenantID == "" {
		return nil, fmt.Errorf("azure tenant ID is required")
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("azure client ID is required")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return nil, fmt.Errorf("azure client secret is required")
	}
	authority, err := url.Parse(cfg.Authority)
	if err != nil {
		return nil, fmt.Errorf("azure authority is not a valid URL: %w", err)
	}
	if authority.Scheme != "https" {
		return nil, fmt.Errorf("azure authority must be an https URL")
	}
	if authority.User != nil || authority.RawQuery != "" || authority.Fragment != "" || strings.Trim(authority.Path, "/") != "" {
		return nil, fmt.Errorf("azure authority must be a bare https host")
	}
	if _, ok := allowedAzureAuthorityHosts[strings.ToLower(authority.Hostname())]; !ok {
		return nil, fmt.Errorf("azure authority host %q is not an allowed Azure AD login endpoint", authority.Hostname())
	}
	return &AzureServicePrincipalTokenSource{cfg: cfg}, nil
}

func (s *AzureServicePrincipalTokenSource) Describe() string {
	if s == nil {
		return "azure service principal (unconfigured)"
	}
	return "azure service principal (tenant " + s.cfg.TenantID + ", client " + s.cfg.ClientID + ")"
}

// Invalidate drops the cached token so the next call re-mints one. Client calls
// this on a 401/403 before its single retry.
func (s *AzureServicePrincipalTokenSource) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
	s.expiresAt = time.Time{}
}

func (s *AzureServicePrincipalTokenSource) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *AzureServicePrincipalTokenSource) Token(ctx context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("azure token source is not initialized")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if s.token != "" && now.Before(s.expiresAt.Add(-tokenRenewalSkew)) {
		return s.token, nil
	}
	token, expiresIn, err := s.mint(ctx)
	if err != nil {
		return "", err
	}
	s.token = token
	s.expiresAt = now.Add(expiresIn)
	return token, nil
}

func (s *AzureServicePrincipalTokenSource) mint(ctx context.Context) (string, time.Duration, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", s.cfg.ClientID)
	form.Set("client_secret", s.cfg.ClientSecret)
	form.Set("scope", s.cfg.Scope)

	endpoint := s.cfg.Authority + "/" + url.PathEscape(s.cfg.TenantID) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := s.cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("request Azure AD token: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenEndpointResponseBytes+1))
	if err != nil {
		return "", 0, fmt.Errorf("read Azure AD token response: %w", err)
	}
	if int64(len(payload)) > maxTokenEndpointResponseBytes {
		return "", 0, fmt.Errorf("azure AD token response exceeds %d bytes", maxTokenEndpointResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The AAD error body carries error/error_description but never the
		// secret, so it is safe to surface for operator diagnosis.
		return "", 0, fmt.Errorf("azure AD token endpoint returned %d: %s", resp.StatusCode, summarizeAADError(payload))
	}
	var decoded struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", 0, fmt.Errorf("decode Azure AD token response: %w", err)
	}
	token := strings.TrimSpace(decoded.AccessToken)
	if token == "" {
		return "", 0, fmt.Errorf("azure AD token response contained no access token")
	}
	expiresIn := time.Duration(decoded.ExpiresIn) * time.Second
	// A missing or implausibly short expires_in must not produce a token that
	// the renewal skew immediately treats as expired (which would re-mint on
	// every single API call); floor it well above the skew.
	if expiresIn < 2*tokenRenewalSkew {
		expiresIn = 2 * tokenRenewalSkew
	}
	return token, expiresIn, nil
}

func summarizeAADError(payload []byte) string {
	var decoded struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(payload, &decoded); err == nil {
		summary := strings.TrimSpace(decoded.Error)
		if desc := strings.TrimSpace(decoded.Description); desc != "" {
			// AAD descriptions are multi-line with correlation IDs; the first
			// line carries the actionable AADSTS code.
			if idx := strings.IndexByte(desc, '\n'); idx > 0 {
				desc = strings.TrimSpace(desc[:idx])
			}
			if summary == "" {
				summary = desc
			} else {
				summary += ": " + desc
			}
		}
		if summary != "" {
			return summary
		}
	}
	return strings.TrimSpace(string(payload))
}
