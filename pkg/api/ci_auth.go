package api

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
	"golang.org/x/oauth2"
)

const (
	ciAPIVersion             = "v1"
	ciBearerPrefix           = "apxci_"
	ciBearerIssuer           = "apexload-controller"
	ciBearerAudience         = "apexload-ci-api"
	ciBearerTTL              = 10 * time.Minute
	ciExchangeRatePerMinute  = 30
	ciOIDCExchangeMaxBody    = 128 << 10
	ciOIDCProviderCacheLimit = 64
	ciOIDCExchangeCacheLimit = 4096
)

type ciOIDCProviderRuntime struct {
	Provider  *oidc.Provider
	CreatedAt time.Time
}

type ciOIDCExchangeCacheEntry struct {
	Token       string    `json:"token"`
	Fingerprint string    `json:"fingerprint"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// githubActionsOIDCClaims lists the immutable claims APEX validates directly.
// The formatted subject is retained for audit only and is never the trust key,
// because GHES and GitHub.com do not promise the same subject shape.
type githubActionsOIDCClaims struct {
	Subject           string `json:"sub"`
	JTI               string `json:"jti"`
	Repository        string `json:"repository"`
	RepositoryID      string `json:"repository_id"`
	Ref               string `json:"ref"`
	SHA               string `json:"sha"`
	WorkflowRef       string `json:"workflow_ref"`
	JobWorkflowRef    string `json:"job_workflow_ref"`
	Environment       string `json:"environment"`
	RunnerEnvironment string `json:"runner_environment"`
	RunID             string `json:"run_id"`
	RunAttempt        string `json:"run_attempt"`
	Actor             string `json:"actor"`
	EventName         string `json:"event_name"`
}

func (c githubActionsOIDCClaims) effectiveWorkflowRef() string {
	// For a reusable workflow, workflow_ref describes the caller while
	// job_workflow_ref identifies the called workflow that actually defines and
	// executes this job. Trust the executable workflow when GitHub supplies it.
	return firstNonEmpty(strings.TrimSpace(c.JobWorkflowRef), strings.TrimSpace(c.WorkflowRef))
}

type ciBearerClaims struct {
	ProjectID         string   `json:"project_id"`
	PolicyRevision    int64    `json:"policy_revision"`
	Repository        string   `json:"repository"`
	RepositoryID      string   `json:"repository_id"`
	Ref               string   `json:"ref"`
	SHA               string   `json:"sha"`
	WorkflowRef       string   `json:"workflow_ref"`
	JobWorkflowRef    string   `json:"job_workflow_ref,omitempty"`
	Environment       string   `json:"environment,omitempty"`
	RunnerEnvironment string   `json:"runner_environment,omitempty"`
	RunID             string   `json:"run_id"`
	RunAttempt        string   `json:"run_attempt"`
	Actor             string   `json:"actor,omitempty"`
	EventName         string   `json:"event_name,omitempty"`
	Scopes            []string `json:"scopes"`
	jwt.RegisteredClaims
}

type ciClaimsContextKey struct{}

func ciClaimsFromContext(ctx context.Context) (*ciBearerClaims, bool) {
	claims, ok := ctx.Value(ciClaimsContextKey{}).(*ciBearerClaims)
	return claims, ok && claims != nil
}

func withCIClaims(r *http.Request, claims *ciBearerClaims) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ciClaimsContextKey{}, claims))
}

func (s *Server) ciOIDCAudience() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("APEXLOAD_CI_OIDC_AUDIENCE")); configured != "" {
		if err := validateCIOIDCAudience(configured); err != nil {
			return "", err
		}
		return configured, nil
	}
	// A public controller URL is shared by every replica and remains stable
	// across restarts, unlike the process-unique controller ID. Hashing its
	// canonical form produces a deployment-specific audience without exposing
	// internal host or path details in GitHub's OIDC token.
	if strings.TrimSpace(os.Getenv("APEXLOAD_PUBLIC_URL")) != "" {
		publicBase, err := ciPublicBaseURL()
		if err != nil {
			return "", fmt.Errorf("derive CI OIDC audience from APEXLOAD_PUBLIC_URL: %w", err)
		}
		parsed, err := url.Parse(publicBase)
		if err != nil {
			return "", fmt.Errorf("derive CI OIDC audience: %w", err)
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		host := strings.ToLower(parsed.Hostname())
		port := parsed.Port()
		if port == "443" {
			port = ""
		}
		if port != "" {
			host = net.JoinHostPort(host, port)
		} else if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		parsed.Host = host
		sum := sha256.Sum256([]byte(parsed.String()))
		return "apexload://ci/" + hex.EncodeToString(sum[:16]), nil
	}
	if productionMode() || (s != nil && s.haExecution) {
		return "", fmt.Errorf("APEXLOAD_CI_OIDC_AUDIENCE or APEXLOAD_PUBLIC_URL is required for a stable deployment-specific CI OIDC audience")
	}
	return "apexload://ci-development", nil
}

func validateCIOIDCAudience(audience string) error {
	if audience == "" || strings.TrimSpace(audience) != audience || len(audience) > 512 {
		return fmt.Errorf("CI OIDC audience is empty, padded, or exceeds 512 bytes")
	}
	if audience == "apexload://ci" {
		return fmt.Errorf("legacy generic CI OIDC audience is not allowed; configure a deployment-specific audience")
	}
	if audience == "apexload://ci-development" {
		return fmt.Errorf("development CI OIDC audience is reserved for the automatic single-controller fallback and cannot be configured explicitly")
	}
	for _, character := range audience {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return fmt.Errorf("CI OIDC audience contains whitespace or control characters")
		}
	}
	return nil
}

func (s *Server) handleCIWellKnown(w http.ResponseWriter, _ *http.Request) {
	audience, err := s.ciOIDCAudience()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "CI OIDC audience is unavailable: "+err.Error())
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"schema_version": 1,
		"controller_id":  strings.TrimSpace(s.controllerID),
		"oidc_audience":  audience,
		"ci_api_version": ciAPIVersion,
		"auth_modes":     []string{"oidc", "api-key"},
	})
}

type githubCIExchangeRequest struct {
	IDToken   string `json:"id_token"`
	ProjectID string `json:"project_id"`
}

type githubCIExchangeResponse struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresIn   int64     `json:"expires_in"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (s *Server) handleGitHubCIExchange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s == nil || s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "CI authentication storage is unavailable")
		return
	}
	if !s.allowGitHubCIExchange(w, r) {
		return
	}
	var req githubCIExchangeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, ciOIDCExchangeMaxBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		s.recordCIExchangeAudit(r, "denied", "invalid_request", "", githubActionsOIDCClaims{})
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ProjectID = normalizeProjectID(req.ProjectID)
	if req.ProjectID == "" || strings.TrimSpace(req.IDToken) == "" {
		s.recordCIExchangeAudit(r, "denied", "missing_required_field", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusBadRequest, "project_id and id_token are required")
		return
	}
	project, err := s.store.GetProject(req.ProjectID)
	if err != nil || project == nil {
		s.recordCIExchangeAudit(r, "denied", "project_not_found", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	policy := normalizeGitHubProjectPolicy(project.Settings.GitHub).Actions
	if !policy.Enabled {
		s.recordCIExchangeAudit(r, "denied", "policy_disabled", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	if err := validateGitHubActionsIssuerServerPair(policy.Issuer, policy.ServerURL); err != nil {
		s.recordCIExchangeAudit(r, "denied", "trust_configuration_invalid", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusServiceUnavailable, "GitHub Actions OIDC trust is misconfigured")
		return
	}
	audience, err := s.ciOIDCAudience()
	if err != nil {
		s.recordCIExchangeAudit(r, "denied", "audience_configuration_invalid", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusServiceUnavailable, "GitHub Actions OIDC audience is unavailable")
		return
	}
	if policy.Audience != "" && policy.Audience != audience {
		s.recordCIExchangeAudit(r, "denied", "audience_configuration_mismatch", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusServiceUnavailable, "GitHub Actions OIDC audience is misconfigured")
		return
	}
	provider, err := s.githubActionsOIDCProvider(r.Context(), policy.Issuer)
	if err != nil {
		s.recordCIExchangeAudit(r, "denied", "oidc_discovery_failed", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusServiceUnavailable, "GitHub Actions OIDC provider is unavailable")
		return
	}
	verifyCtx, cancel := context.WithTimeout(r.Context(), defaultOIDCDiscoveryTimeout)
	defer cancel()
	idToken, err := provider.Provider.Verifier(&oidc.Config{ClientID: audience}).Verify(verifyCtx, strings.TrimSpace(req.IDToken))
	if err != nil {
		s.recordCIExchangeAudit(r, "denied", "invalid_id_token", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	// go-oidc's ClientID check is intentionally membership-based: it accepts a
	// token when the requested audience is one member of a multi-valued aud
	// claim. GitHub Actions issues this controller token for one exact audience,
	// so enforce that stronger contract independently.
	if len(idToken.Audience) != 1 || idToken.Audience[0] != audience {
		s.recordCIExchangeAudit(r, "denied", "invalid_audience", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	var claims githubActionsOIDCClaims
	if err := idToken.Claims(&claims); err != nil {
		s.recordCIExchangeAudit(r, "denied", "invalid_claims", req.ProjectID, githubActionsOIDCClaims{})
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	claims.Subject = firstNonEmpty(claims.Subject, idToken.Subject)
	if err := validateBoundedGitHubActionsClaim("jti", claims.JTI, 512, true); err != nil {
		s.recordCIExchangeAudit(r, "denied", "invalid_jti", req.ProjectID, claims)
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	if err := validateGitHubActionsIdentity(policy, claims); err != nil {
		s.recordCIExchangeAudit(r, "denied", "policy_rejected", req.ProjectID, claims)
		writeError(w, http.StatusForbidden, "GitHub Actions identity is not allowed by project policy")
		return
	}
	if err := s.verifyGitHubActionsPullRequestOrigin(r.Context(), project.Settings.GitHub, policy, claims); err != nil {
		s.recordCIExchangeAudit(r, "denied", "fork_or_unverifiable_pull_request", req.ProjectID, claims)
		writeError(w, http.StatusForbidden, "pull request origin could not be trusted")
		return
	}
	if strings.TrimSpace(claims.JTI) == "" {
		s.recordCIExchangeAudit(r, "denied", "missing_jti", req.ProjectID, claims)
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	fingerprintSum := sha256.Sum256([]byte(strings.TrimSpace(req.IDToken)))
	fingerprint := hex.EncodeToString(fingerprintSum[:])
	cacheKey := ciOIDCExchangeKey(policy.Issuer, req.ProjectID, claims.JTI)
	if s.ciExchangeRequiresRemoteCache() {
		s.recordCIExchangeAudit(r, "denied", "exchange_cache_not_ha_safe", req.ProjectID, claims)
		writeError(w, http.StatusServiceUnavailable, "CI authentication replay protection requires Redis or Valkey when controller HA is enabled")
		return
	}
	if cached, ok, replay := s.cachedCIExchange(cacheKey, fingerprint); ok {
		if replay {
			s.recordCIExchangeAudit(r, "denied", "jti_replay", req.ProjectID, claims)
			writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
			return
		}
		s.recordCIExchangeAudit(r, "success", "idempotent", req.ProjectID, claims)
		writeJSON(w, http.StatusOK, githubCIExchangeResponse{AccessToken: cached.Token, TokenType: "Bearer", ExpiresIn: maxInt64(0, int64(time.Until(cached.ExpiresAt).Seconds())), ExpiresAt: cached.ExpiresAt})
		return
	}
	token, expiresAt, err := s.issueCIBearerToken(req.ProjectID, policy, claims)
	if err != nil {
		s.recordCIExchangeAudit(r, "denied", "token_signing_unavailable", req.ProjectID, claims)
		writeError(w, http.StatusServiceUnavailable, "CI token signing is unavailable")
		return
	}
	actual, loaded, err := s.loadOrStoreCIExchange(r.Context(), cacheKey, ciOIDCExchangeCacheEntry{Token: token, Fingerprint: fingerprint, ExpiresAt: expiresAt})
	if err != nil {
		s.recordCIExchangeAudit(r, "denied", "exchange_cache_unavailable", req.ProjectID, claims)
		writeError(w, http.StatusServiceUnavailable, "CI authentication replay protection is unavailable")
		return
	}
	if actual.Fingerprint != fingerprint {
		s.recordCIExchangeAudit(r, "denied", "jti_replay", req.ProjectID, claims)
		writeError(w, http.StatusUnauthorized, "GitHub Actions authentication denied")
		return
	}
	// The local map is only a bounded hot cache. Populate it after the shared
	// backend has atomically selected the authoritative exchange result.
	s.storeCIExchange(cacheKey, actual.Fingerprint, actual.Token, actual.ExpiresAt)
	reason := "issued"
	if loaded {
		reason = "idempotent"
	}
	s.recordCIExchangeAudit(r, "success", reason, req.ProjectID, claims)
	writeJSON(w, http.StatusOK, githubCIExchangeResponse{AccessToken: actual.Token, TokenType: "Bearer", ExpiresIn: maxInt64(0, int64(time.Until(actual.ExpiresAt).Seconds())), ExpiresAt: actual.ExpiresAt})
}

func (s *Server) ciExchangeRequiresRemoteCache() bool {
	if s == nil || !s.haExecution {
		return false
	}
	if s.cache == nil {
		return true
	}
	return !cacheBackendIsRemote(s.cache.Info().Backend)
}

func (s *Server) allowGitHubCIExchange(w http.ResponseWriter, r *http.Request) bool {
	if s == nil || s.cache == nil {
		return true
	}
	peer := "unknown"
	if ip := requestSourceIP(r); ip != nil {
		peer = ip.String()
	}
	allowed, remaining, err := s.cache.AllowRateLimit(r.Context(), "ci-oidc-exchange:"+peer, ciExchangeRatePerMinute, time.Minute)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "CI authentication rate limit is unavailable")
		return false
	}
	w.Header().Set("X-RateLimit-Limit", "30")
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "CI authentication rate limit exceeded")
		return false
	}
	return true
}

func (s *Server) githubActionsOIDCProvider(ctx context.Context, issuer string) (*ciOIDCProviderRuntime, error) {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if err := validateGitHubActionsTrustURL("issuer", issuer); err != nil {
		return nil, err
	}
	s.ciAuthMu.Lock()
	if cached := s.ciOIDCProviders[issuer]; cached != nil {
		s.ciAuthMu.Unlock()
		return cached, nil
	}
	s.ciAuthMu.Unlock()
	client, err := newGitHubActionsOIDCHTTPClient()
	if err != nil {
		return nil, err
	}
	discoveryCtx, cancel := context.WithTimeout(context.WithValue(ctx, oauth2.HTTPClient, client), defaultOIDCDiscoveryTimeout)
	defer cancel()
	provider, err := newOIDCProvider(discoveryCtx, issuer)
	if err != nil {
		return nil, err
	}
	runtime := &ciOIDCProviderRuntime{Provider: provider, CreatedAt: time.Now().UTC()}
	s.ciAuthMu.Lock()
	defer s.ciAuthMu.Unlock()
	if cached := s.ciOIDCProviders[issuer]; cached != nil {
		return cached, nil
	}
	if len(s.ciOIDCProviders) >= ciOIDCProviderCacheLimit {
		var oldestKey string
		var oldest time.Time
		for key, entry := range s.ciOIDCProviders {
			if oldestKey == "" || entry.CreatedAt.Before(oldest) {
				oldestKey, oldest = key, entry.CreatedAt
			}
		}
		delete(s.ciOIDCProviders, oldestKey)
	}
	s.ciOIDCProviders[issuer] = runtime
	return runtime, nil
}

func newGitHubActionsOIDCHTTPClient() (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = oidcResponseHeaderMaxBytes
	if caFile := strings.TrimSpace(os.Getenv("APEXLOAD_CI_OIDC_CA_CERT_FILE")); caFile != "" {
		pemBytes, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("read CI OIDC CA bundle: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("CI OIDC CA bundle contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	return &http.Client{Timeout: defaultOIDCDiscoveryTimeout, Transport: oidcBoundedRoundTripper{base: transport}, CheckRedirect: sameOriginRedirectPolicy}, nil
}

func validateGitHubActionsTrustURL(label, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("GitHub Actions %s must be an absolute URL without credentials, query, or fragment", label)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && !productionMode() {
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") || net.ParseIP(host).IsLoopback() || envBool(githubAllowInsecureHTTPEnv) {
			return nil
		}
	}
	return fmt.Errorf("GitHub Actions %s must use HTTPS", label)
}

func validateGitHubActionsIssuerServerPair(issuerRaw, serverRaw string) error {
	issuerRaw = strings.TrimSpace(issuerRaw)
	serverRaw = strings.TrimSpace(serverRaw)
	if err := validateGitHubActionsTrustURL("issuer", issuerRaw); err != nil {
		return err
	}
	if err := validateGitHubActionsTrustURL("server URL", serverRaw); err != nil {
		return err
	}
	issuer, issuerErr := url.Parse(issuerRaw)
	server, serverErr := url.Parse(serverRaw)
	if issuerErr != nil || serverErr != nil {
		return fmt.Errorf("GitHub Actions issuer and server URL must be valid absolute URLs")
	}
	if server.Path != "" && server.Path != "/" {
		return fmt.Errorf("GitHub Actions server URL must identify the GitHub origin without a path")
	}
	if server.RawPath != "" && server.EscapedPath() != "/" {
		return fmt.Errorf("GitHub Actions server URL must identify the GitHub origin without an encoded path")
	}
	if strings.EqualFold(server.Hostname(), "github.com") {
		if serverRaw != "https://github.com" || issuerRaw != defaultGitHubActionsOIDCIssuer {
			return fmt.Errorf("GitHub.com Actions requires server URL https://github.com and issuer %s", defaultGitHubActionsOIDCIssuer)
		}
		return nil
	}
	if !sameGitHubActionsURLOrigin(issuer, server) {
		return fmt.Errorf("GHES Actions issuer and server URL must use the same origin")
	}
	if issuer.Path != "/_services/token" || issuer.EscapedPath() != "/_services/token" {
		return fmt.Errorf("GHES Actions issuer path must be exactly /_services/token")
	}
	return nil
}

func sameGitHubActionsURLOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || left.Hostname() == "" || right.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(left.Scheme, right.Scheme) || !strings.EqualFold(left.Hostname(), right.Hostname()) {
		return false
	}
	return effectiveGitHubActionsURLPort(left) == effectiveGitHubActionsURLPort(right)
}

func effectiveGitHubActionsURLPort(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	if port := parsed.Port(); port != "" {
		return port
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

func validateGitHubActionsIdentity(policy storage.GitHubActionsPolicy, claims githubActionsOIDCClaims) error {
	if !policy.Enabled {
		return fmt.Errorf("GitHub Actions policy is disabled")
	}
	if err := validateGitHubActionsClaimShape(claims); err != nil {
		return err
	}
	repository := strings.ToLower(strings.TrimSpace(claims.Repository))
	repositoryID := strings.TrimSpace(claims.RepositoryID)
	if len(policy.AllowedRepositoryIDs) > 0 && !stringSetContains(policy.AllowedRepositoryIDs, repositoryID, false) {
		return fmt.Errorf("repository id is not allowed")
	}
	if len(policy.AllowedRepositories) > 0 && !githubPolicyAllowsPattern(repository, policy.AllowedRepositories, true) {
		return fmt.Errorf("repository is not allowed")
	}
	if !githubPolicyAllowsPattern(claims.Ref, policy.AllowedRefs, false) {
		return fmt.Errorf("ref is not allowed")
	}
	if !githubPolicyAllowsPattern(claims.effectiveWorkflowRef(), policy.AllowedWorkflowRefs, false) {
		return fmt.Errorf("workflow ref is not allowed")
	}
	if len(policy.AllowedEnvironments) > 0 && !githubPolicyAllowsPattern(claims.Environment, policy.AllowedEnvironments, false) {
		return fmt.Errorf("environment is not allowed")
	}
	if policy.RequiredRunnerEnvironment != "" && !strings.EqualFold(policy.RequiredRunnerEnvironment, strings.TrimSpace(claims.RunnerEnvironment)) {
		return fmt.Errorf("runner environment is not allowed")
	}
	if strings.EqualFold(strings.TrimSpace(claims.EventName), "pull_request_target") {
		return fmt.Errorf("pull_request_target is not permitted for CI execution")
	}
	return nil
}

func validateGitHubActionsClaimShape(claims githubActionsOIDCClaims) error {
	bounded := []struct {
		name     string
		value    string
		maxBytes int
		required bool
	}{
		{"repository", claims.Repository, 256, true},
		{"repository_id", claims.RepositoryID, 32, true},
		{"ref", claims.Ref, 1024, true},
		{"sha", claims.SHA, 64, true},
		{"workflow_ref", claims.WorkflowRef, 2048, false},
		{"job_workflow_ref", claims.JobWorkflowRef, 2048, false},
		{"environment", claims.Environment, 256, false},
		{"runner_environment", claims.RunnerEnvironment, 64, false},
		{"run_id", claims.RunID, 32, true},
		{"run_attempt", claims.RunAttempt, 20, true},
		{"actor", claims.Actor, 256, true},
		{"event_name", claims.EventName, 128, true},
	}
	for _, claim := range bounded {
		if err := validateBoundedGitHubActionsClaim(claim.name, claim.value, claim.maxBytes, claim.required); err != nil {
			return err
		}
	}
	if strings.TrimSpace(claims.effectiveWorkflowRef()) == "" {
		return fmt.Errorf("required GitHub Actions workflow identity claim is missing")
	}
	owner, repo, ok := splitOwnerRepo(claims.Repository)
	if !ok || validateGitHubRepositoryCoordinates(owner, repo) != nil {
		return fmt.Errorf("repository claim is invalid")
	}
	if !validPositiveGitHubActionsNumber(claims.RepositoryID) {
		return fmt.Errorf("repository_id claim must be a positive decimal integer")
	}
	if !validPositiveGitHubActionsNumber(claims.RunID) {
		return fmt.Errorf("run_id claim must be a positive decimal integer")
	}
	if !validPositiveGitHubActionsNumber(claims.RunAttempt) {
		return fmt.Errorf("run_attempt claim must be a positive decimal integer")
	}
	if !validGitHubActionsSHA(claims.SHA) {
		return fmt.Errorf("sha claim must be a 40- or 64-character hexadecimal object id")
	}
	if !validGitHubActionsRef(claims.Ref) {
		return fmt.Errorf("ref claim is invalid")
	}
	if claims.WorkflowRef != "" && !validGitHubActionsWorkflowRef(claims.WorkflowRef) {
		return fmt.Errorf("workflow_ref claim is invalid")
	}
	if claims.JobWorkflowRef != "" && !validGitHubActionsWorkflowRef(claims.JobWorkflowRef) {
		return fmt.Errorf("job_workflow_ref claim is invalid")
	}
	return nil
}

func validateBoundedGitHubActionsClaim(name, value string, maxBytes int, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("required GitHub Actions identity claim %s is missing", name)
		}
		return nil
	}
	if strings.TrimSpace(value) != value || len(value) > maxBytes {
		return fmt.Errorf("GitHub Actions identity claim %s is invalid or exceeds %d bytes", name, maxBytes)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("GitHub Actions identity claim %s contains control characters", name)
		}
	}
	return nil
}

func validPositiveGitHubActionsNumber(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 63)
	return err == nil && parsed > 0
}

func validGitHubActionsSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

func validGitHubActionsRef(value string) bool {
	if !strings.HasPrefix(value, "refs/") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") ||
		strings.Contains(value, "//") || strings.Contains(value, "..") || strings.Contains(value, "@{") ||
		strings.ContainsAny(value, " ~^:?*[\\") {
		return false
	}
	return true
}

func validGitHubActionsWorkflowRef(value string) bool {
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 || !validGitHubActionsRef(value[at+1:]) {
		return false
	}
	path := value[:at]
	marker := "/.github/workflows/"
	markerIndex := strings.Index(path, marker)
	if markerIndex <= 0 || strings.Contains(path[markerIndex+len(marker):], "/") {
		return false
	}
	owner, repo, ok := splitOwnerRepo(path[:markerIndex])
	if !ok || validateGitHubRepositoryCoordinates(owner, repo) != nil {
		return false
	}
	workflowFile := path[markerIndex+len(marker):]
	if workflowFile == "" || strings.ContainsAny(workflowFile, `\\?#`) || workflowFile == "." || workflowFile == ".." {
		return false
	}
	lower := strings.ToLower(workflowFile)
	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

// GitHub's OIDC token identifies the base repository but does not carry an
// immutable head-repository id for pull requests. Verify the workflow-run record
// through the controller App before exchanging a PR token, and fail closed when
// that record cannot prove the head repository equals the base repository.
func (s *Server) verifyGitHubActionsPullRequestOrigin(ctx context.Context, projectPolicy storage.GitHubProjectPolicy, actionsPolicy storage.GitHubActionsPolicy, claims githubActionsOIDCClaims) error {
	event := strings.ToLower(strings.TrimSpace(claims.EventName))
	if event == "pull_request_target" {
		return fmt.Errorf("pull_request_target is prohibited")
	}
	if event != "pull_request" {
		return nil
	}
	owner, repo, ok := splitOwnerRepo(claims.Repository)
	if !ok {
		return fmt.Errorf("invalid repository claim")
	}
	settings := s.currentGitHubSettings()
	if !sameURLOrigin(settings.WebBaseURL, actionsPolicy.ServerURL) || !trustedGitHubAPIURL(actionsPolicy.ServerURL, settings.APIBaseURL) {
		return fmt.Errorf("GitHub App server does not match the GitHub Actions policy server")
	}
	if !settings.Enabled || !settings.appConfigured() || projectPolicy.AppInstallationID <= 0 {
		return fmt.Errorf("GitHub App verification is required for pull requests")
	}
	client, err := s.githubInstallationClient(ctx, settings, projectPolicy.AppInstallationID, owner, repo)
	if err != nil {
		return err
	}
	run, err := client.GetWorkflowRun(ctx, owner, repo, claims.RunID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(run.Event, "pull_request") {
		return fmt.Errorf("workflow run event does not match token event")
	}
	claimID := strings.TrimSpace(claims.RepositoryID)
	baseID := fmt.Sprintf("%d", run.Repository.ID)
	headID := fmt.Sprintf("%d", run.HeadRepository.ID)
	if run.Repository.ID <= 0 || run.HeadRepository.ID <= 0 || baseID != claimID || headID != claimID {
		return fmt.Errorf("fork pull requests are not permitted")
	}
	if !strings.EqualFold(strings.TrimSpace(run.Repository.FullName), strings.TrimSpace(claims.Repository)) || !strings.EqualFold(strings.TrimSpace(run.HeadRepository.FullName), strings.TrimSpace(claims.Repository)) {
		return fmt.Errorf("pull request repositories do not match the trusted repository")
	}
	return nil
}

func stringSetContains(values []string, value string, fold bool) bool {
	value = strings.TrimSpace(value)
	for _, candidate := range values {
		if fold && strings.EqualFold(strings.TrimSpace(candidate), value) {
			return true
		}
		if !fold && strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}

func (s *Server) ciTokenSigningKey() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv("APEXLOAD_CI_TOKEN_KEY"))
	if secret == "" && !productionMode() {
		secret = strings.TrimSpace(os.Getenv("APEXLOAD_AUTH_SESSION_SECRET"))
		if secret == "" {
			secret = "apexload-development-ci-token-key:" + strings.TrimSpace(s.controllerID)
		}
	}
	if len([]byte(secret)) < productionSecretMinBytes {
		return nil, fmt.Errorf("APEXLOAD_CI_TOKEN_KEY must contain at least %d bytes", productionSecretMinBytes)
	}
	return []byte(secret), nil
}

func (s *Server) issueCIBearerToken(projectID string, policy storage.GitHubActionsPolicy, identity githubActionsOIDCClaims) (string, time.Time, error) {
	key, err := s.ciTokenSigningKey()
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(ciBearerTTL)
	claims := ciBearerClaims{
		ProjectID: projectID, PolicyRevision: policy.Revision,
		Repository: strings.ToLower(strings.TrimSpace(identity.Repository)), RepositoryID: strings.TrimSpace(identity.RepositoryID),
		Ref: strings.TrimSpace(identity.Ref), SHA: strings.TrimSpace(identity.SHA), WorkflowRef: strings.TrimSpace(identity.WorkflowRef), JobWorkflowRef: strings.TrimSpace(identity.JobWorkflowRef),
		Environment: strings.TrimSpace(identity.Environment), RunnerEnvironment: strings.TrimSpace(identity.RunnerEnvironment),
		RunID: strings.TrimSpace(identity.RunID), RunAttempt: strings.TrimSpace(identity.RunAttempt), Actor: strings.TrimSpace(identity.Actor), EventName: strings.TrimSpace(identity.EventName),
		Scopes: []string{"tests:run", "results:read"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: ciBearerIssuer, Subject: "github-actions:" + strings.TrimSpace(identity.RepositoryID), Audience: jwt.ClaimStrings{ciBearerAudience},
			ExpiresAt: jwt.NewNumericDate(expiresAt), NotBefore: jwt.NewNumericDate(now.Add(-30 * time.Second)), IssuedAt: jwt.NewNumericDate(now), ID: uuid.NewString(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		return "", time.Time{}, err
	}
	return ciBearerPrefix + signed, expiresAt, nil
}

func extractCIBearer(r *http.Request) (string, bool) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	return token, strings.HasPrefix(token, ciBearerPrefix)
}

func (s *Server) verifyCIBearer(raw string) (*ciBearerClaims, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("CI authentication storage is unavailable")
	}
	if !strings.HasPrefix(raw, ciBearerPrefix) {
		return nil, fmt.Errorf("invalid CI bearer token")
	}
	key, err := s.ciTokenSigningKey()
	if err != nil {
		return nil, err
	}
	claims := &ciBearerClaims{}
	parsed, err := jwt.ParseWithClaims(strings.TrimPrefix(raw, ciBearerPrefix), claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected CI token signing method")
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(ciBearerIssuer), jwt.WithAudience(ciBearerAudience), jwt.WithLeeway(30*time.Second))
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("invalid or expired CI bearer token")
	}
	project, err := s.store.GetProject(normalizeProjectID(claims.ProjectID))
	if err != nil || project == nil {
		return nil, fmt.Errorf("CI token project is unavailable")
	}
	policy := normalizeGitHubProjectPolicy(project.Settings.GitHub).Actions
	if !policy.Enabled || policy.Revision != claims.PolicyRevision {
		return nil, fmt.Errorf("CI token was invalidated by a project policy change")
	}
	if err := validateGitHubActionsIssuerServerPair(policy.Issuer, policy.ServerURL); err != nil {
		return nil, fmt.Errorf("CI token project policy has invalid GitHub Actions trust configuration")
	}
	identity := githubActionsOIDCClaims{Repository: claims.Repository, RepositoryID: claims.RepositoryID, Ref: claims.Ref, SHA: claims.SHA, WorkflowRef: claims.WorkflowRef, JobWorkflowRef: claims.JobWorkflowRef, Environment: claims.Environment, RunnerEnvironment: claims.RunnerEnvironment, RunID: claims.RunID, RunAttempt: claims.RunAttempt, Actor: claims.Actor, EventName: claims.EventName}
	if err := validateGitHubActionsIdentity(policy, identity); err != nil {
		return nil, fmt.Errorf("CI token no longer satisfies project policy")
	}
	if !stringSetContains(claims.Scopes, "tests:run", false) || !stringSetContains(claims.Scopes, "results:read", false) {
		return nil, fmt.Errorf("CI bearer token is missing required scopes")
	}
	return claims, nil
}

func (s *Server) ciAuthMiddleware(fallback func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fallbackHandler := fallback(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := extractCIBearer(r)
			if !ok {
				fallbackHandler.ServeHTTP(w, r)
				return
			}
			claims, err := s.verifyCIBearer(raw)
			if err != nil {
				s.recordAuditEvent(r, "ci.token.authenticate", "denied", "project", "", "", map[string]any{"reason": err.Error()})
				writeError(w, http.StatusUnauthorized, "CI authentication required")
				return
			}
			resolution := authResolution{
				User:           &AuthenticatedUser{Subject: "github-actions:" + claims.RepositoryID, DisplayName: firstNonEmpty(claims.Actor, claims.Repository), GlobalRoles: map[string]struct{}{}, ProjectRoles: map[string]map[string]struct{}{normalizeProjectID(claims.ProjectID): {RoleCIRunner: {}}}, DomainRoles: map[string]map[string]struct{}{}, RepositoryRoles: map[string]map[string]struct{}{}},
				ServiceAccount: &AuthenticatedServiceAccount{ID: "github-actions:" + claims.RepositoryID + ":" + claims.RunID, Name: "GitHub Actions " + claims.Repository, Description: "OIDC-exchanged project CI identity", Scopes: append([]string(nil), claims.Scopes...)},
			}
			requirement, found := accessRequirementForRequest(r)
			if !found || !authorizeAccessRequirement(w, r, s.store, resolution, requirement) {
				return
			}
			r = withCIClaims(withAuthenticatedContext(r, resolution), claims)
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) cachedCIExchange(key, fingerprint string) (ciOIDCExchangeCacheEntry, bool, bool) {
	// An HA replica must confirm every exchange against the shared atomic
	// backend. A locally cached success must not keep authentication available
	// after Redis/Valkey becomes unreachable or is replaced.
	if s == nil || s.haExecution {
		return ciOIDCExchangeCacheEntry{}, false, false
	}
	now := time.Now().UTC()
	s.ciAuthMu.Lock()
	defer s.ciAuthMu.Unlock()
	entry, ok := s.ciOIDCExchanges[key]
	if !ok || !entry.ExpiresAt.After(now) {
		delete(s.ciOIDCExchanges, key)
		return ciOIDCExchangeCacheEntry{}, false, false
	}
	return entry, true, entry.Fingerprint != fingerprint
}

func ciOIDCExchangeKey(issuer, projectID, jti string) string {
	hasher := sha256.New()
	for _, part := range []string{strings.TrimRight(strings.TrimSpace(issuer), "/"), normalizeProjectID(projectID), strings.TrimSpace(jti)} {
		_, _ = fmt.Fprintf(hasher, "%d:", len(part))
		_, _ = hasher.Write([]byte(part))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func (s *Server) loadOrStoreCIExchange(ctx context.Context, key string, candidate ciOIDCExchangeCacheEntry) (ciOIDCExchangeCacheEntry, bool, error) {
	if s == nil || s.cache == nil {
		return ciOIDCExchangeCacheEntry{}, false, fmt.Errorf("CI exchange cache is unavailable")
	}
	ttl := time.Until(candidate.ExpiresAt)
	if ttl <= 0 {
		return ciOIDCExchangeCacheEntry{}, false, fmt.Errorf("CI exchange token has already expired")
	}
	payload, err := json.Marshal(candidate)
	if err != nil {
		return ciOIDCExchangeCacheEntry{}, false, fmt.Errorf("encode CI exchange cache entry: %w", err)
	}
	actualPayload, loaded, err := s.cache.LoadOrStoreValue(ctx, key, payload, ttl)
	if err != nil {
		return ciOIDCExchangeCacheEntry{}, false, err
	}
	var actual ciOIDCExchangeCacheEntry
	if err := json.Unmarshal(actualPayload, &actual); err != nil {
		return ciOIDCExchangeCacheEntry{}, false, fmt.Errorf("decode CI exchange cache entry: %w", err)
	}
	if strings.TrimSpace(actual.Token) == "" || !strings.HasPrefix(actual.Token, ciBearerPrefix) || strings.TrimSpace(actual.Fingerprint) == "" || !actual.ExpiresAt.After(time.Now().UTC()) {
		return ciOIDCExchangeCacheEntry{}, false, fmt.Errorf("CI exchange cache entry is invalid or expired")
	}
	return actual, loaded, nil
}

func (s *Server) storeCIExchange(key, fingerprint, token string, expiresAt time.Time) {
	now := time.Now().UTC()
	s.ciAuthMu.Lock()
	defer s.ciAuthMu.Unlock()
	for cacheKey, entry := range s.ciOIDCExchanges {
		if !entry.ExpiresAt.After(now) {
			delete(s.ciOIDCExchanges, cacheKey)
		}
	}
	if len(s.ciOIDCExchanges) >= ciOIDCExchangeCacheLimit {
		var oldestKey string
		var oldest time.Time
		for cacheKey, entry := range s.ciOIDCExchanges {
			if oldestKey == "" || entry.ExpiresAt.Before(oldest) {
				oldestKey, oldest = cacheKey, entry.ExpiresAt
			}
		}
		delete(s.ciOIDCExchanges, oldestKey)
	}
	s.ciOIDCExchanges[key] = ciOIDCExchangeCacheEntry{Token: token, Fingerprint: fingerprint, ExpiresAt: expiresAt}
}

func (s *Server) recordCIExchangeAudit(r *http.Request, status, reason, projectID string, claims githubActionsOIDCClaims) {
	details := map[string]any{"reason": reason}
	if claims.Repository != "" {
		details["repository"] = claims.Repository
	}
	if claims.RepositoryID != "" {
		details["repository_id"] = claims.RepositoryID
	}
	if claims.Ref != "" {
		details["ref"] = claims.Ref
	}
	if claims.effectiveWorkflowRef() != "" {
		details["workflow_ref"] = claims.effectiveWorkflowRef()
	}
	if claims.WorkflowRef != "" && claims.JobWorkflowRef != "" {
		details["caller_workflow_ref"] = claims.WorkflowRef
		details["job_workflow_ref"] = claims.JobWorkflowRef
	}
	if claims.RunID != "" {
		details["run_id"] = claims.RunID
	}
	s.recordAuditEvent(r, "ci.github_oidc.exchange", status, "project", projectID, projectID, details)
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
