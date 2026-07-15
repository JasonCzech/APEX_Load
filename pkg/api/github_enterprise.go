package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	githubInstallationTokenSkew            = 5 * time.Minute
	githubInstallationTokenCacheMaxEntries = 1024
)

type githubCachedToken struct {
	Token          string
	ExpiresAt      time.Time
	InstallationID int64
	Repo           string
}

type githubResolvedClient struct {
	Client   *githubClient
	Settings GitHubSettings
	Policy   storage.GitHubProjectPolicy
	AuthMode string
}

func (s *Server) githubEnterpriseStore() (storage.GitHubEnterpriseStore, bool) {
	store, ok := s.store.(storage.GitHubEnterpriseStore)
	return store, ok
}

func githubAppPrivateKeyPEM(settings GitHubSettings) (string, error) {
	if strings.TrimSpace(settings.AppPrivateKeyEnvVar) != "" {
		value := strings.TrimSpace(envValue(settings.AppPrivateKeyEnvVar))
		if value == "" {
			return "", fmt.Errorf("github app private key env var %s is empty", settings.AppPrivateKeyEnvVar)
		}
		return strings.ReplaceAll(value, `\n`, "\n"), nil
	}
	if strings.TrimSpace(settings.AppPrivateKeyFile) != "" {
		data, err := os.ReadFile(settings.AppPrivateKeyFile)
		if err != nil {
			return "", fmt.Errorf("github app private key file: %w", err)
		}
		return string(data), nil
	}
	return "", fmt.Errorf("github app private key is not configured")
}

func parseGitHubAppPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("github app private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app private key parse failed: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github app private key must be RSA")
	}
	return key, nil
}

func createGitHubAppJWT(settings GitHubSettings, now time.Time) (string, error) {
	if settings.AppID <= 0 {
		return "", fmt.Errorf("github app id is not configured")
	}
	pemText, err := githubAppPrivateKeyPEM(settings)
	if err != nil {
		return "", err
	}
	key, err := parseGitHubAppPrivateKey(pemText)
	if err != nil {
		return "", err
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	payload, _ := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprint(settings.AppID),
	})
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Server) githubAppClient(ctx context.Context, settings GitHubSettings) (*githubClient, error) {
	jwt, err := createGitHubAppJWT(settings, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return newGitHubClient(settings, jwt, githubClientTimeout)
}

func (s *Server) githubInstallationClient(ctx context.Context, settings GitHubSettings, installationID int64, owner, repo string) (*githubClient, error) {
	if installationID <= 0 {
		return nil, fmt.Errorf("github app installation id is not configured for this project")
	}
	cacheKey := fmt.Sprintf("%d:%s/%s", installationID, strings.ToLower(owner), strings.ToLower(repo))
	now := time.Now().UTC()
	if cached, ok := s.githubCachedInstallationToken(cacheKey, now); ok {
		return newGitHubClient(settings, cached.Token, githubClientTimeout)
	}

	appClient, err := s.githubAppClient(ctx, settings)
	if err != nil {
		return nil, err
	}
	repos := []string{}
	if strings.TrimSpace(repo) != "" {
		repos = []string{repo}
	}
	token, err := appClient.CreateInstallationToken(ctx, installationID, repos)
	if err != nil {
		return nil, err
	}
	s.cacheGitHubInstallationToken(cacheKey, githubCachedToken{Token: token.Token, ExpiresAt: token.ExpiresAt, InstallationID: installationID, Repo: owner + "/" + repo}, time.Now().UTC())
	return newGitHubClient(settings, token.Token, githubClientTimeout)
}

func (s *Server) githubCachedInstallationToken(cacheKey string, now time.Time) (githubCachedToken, bool) {
	s.githubMu.Lock()
	defer s.githubMu.Unlock()

	s.pruneGitHubInstallationTokensLocked(now)
	cached, ok := s.githubTokens[cacheKey]
	return cached, ok
}

func (s *Server) cacheGitHubInstallationToken(cacheKey string, cached githubCachedToken, now time.Time) {
	s.githubMu.Lock()
	defer s.githubMu.Unlock()

	s.pruneGitHubInstallationTokensLocked(now)
	if cached.Token == "" || !now.Before(cached.ExpiresAt.Add(-githubInstallationTokenSkew)) {
		return
	}
	if s.githubTokens == nil {
		s.githubTokens = make(map[string]githubCachedToken)
	}
	if _, exists := s.githubTokens[cacheKey]; !exists {
		for len(s.githubTokens) >= githubInstallationTokenCacheMaxEntries {
			s.evictGitHubInstallationTokenLocked()
		}
	}
	s.githubTokens[cacheKey] = cached
}

func (s *Server) pruneGitHubInstallationTokensLocked(now time.Time) {
	for cacheKey, cached := range s.githubTokens {
		if cached.Token == "" || !now.Before(cached.ExpiresAt.Add(-githubInstallationTokenSkew)) {
			delete(s.githubTokens, cacheKey)
		}
	}
}

func (s *Server) evictGitHubInstallationTokenLocked() {
	var evictKey string
	var evictExpiry time.Time
	for cacheKey, cached := range s.githubTokens {
		if evictKey == "" || cached.ExpiresAt.Before(evictExpiry) || (cached.ExpiresAt.Equal(evictExpiry) && cacheKey < evictKey) {
			evictKey = cacheKey
			evictExpiry = cached.ExpiresAt
		}
	}
	if evictKey != "" {
		delete(s.githubTokens, evictKey)
	}
}

func (s *Server) githubProjectPolicy(projectID string) storage.GitHubProjectPolicy {
	projectID = normalizeProjectID(projectID)
	if s.store == nil {
		return storage.GitHubProjectPolicy{}
	}
	project, err := s.store.GetProject(projectID)
	if err != nil || project == nil {
		return storage.GitHubProjectPolicy{}
	}
	return normalizeGitHubProjectPolicy(project.Settings.GitHub)
}

func normalizeGitHubProjectPolicy(policy storage.GitHubProjectPolicy) storage.GitHubProjectPolicy {
	out := policy
	out.DefaultRepo = strings.TrimSpace(out.DefaultRepo)
	out.DefaultBranch = strings.TrimSpace(out.DefaultBranch)
	out.PathTemplate = strings.TrimSpace(out.PathTemplate)
	out.DefaultSyncDirectory = strings.Trim(strings.TrimSpace(out.DefaultSyncDirectory), "/")
	out.AutomationIdentity = strings.TrimSpace(out.AutomationIdentity)
	out.AllowedRepos = normalizeStringSet(out.AllowedRepos, true)
	out.AllowedBranches = normalizeStringSet(out.AllowedBranches, false)
	if out.Enabled {
		if !out.AllowDirectCommit && !out.RequirePullRequest {
			out.RequirePullRequest = true
		}
		if !out.AllowImport && !out.AllowPush {
			out.AllowImport = true
			out.AllowPush = true
		}
	}
	return out
}

func normalizeStringSet(values []string, lower bool) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if lower {
			trimmed = strings.ToLower(trimmed)
		}
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func githubPolicyAllowsPattern(value string, patterns []string, lower bool) bool {
	if len(patterns) == 0 {
		return true
	}
	check := strings.TrimSpace(value)
	if lower {
		check = strings.ToLower(check)
	}
	for _, pattern := range patterns {
		if pattern == check {
			return true
		}
		if ok, _ := path.Match(pattern, check); ok {
			return true
		}
	}
	return false
}

func githubPolicyStaticPrefix(template string) string {
	idx := strings.Index(template, "{")
	if idx >= 0 {
		template = template[:idx]
	}
	return strings.Trim(strings.TrimSpace(template), "/")
}

func enforceGitHubProjectPolicy(policy storage.GitHubProjectPolicy, operation, owner, repo, branch, filePath, strategy string) error {
	if err := validateGitHubRepositoryCoordinates(owner, repo); err != nil {
		return err
	}
	if strings.TrimSpace(branch) != "" {
		if err := validateGitHubContentPath(branch, false); err != nil {
			return fmt.Errorf("invalid github branch: %w", err)
		}
	}
	if operation != "check" && strings.TrimSpace(filePath) != "" {
		if err := validateGitHubContentPath(filePath, false); err != nil {
			return err
		}
	}
	if !policy.Enabled {
		return nil
	}
	fullRepo := owner + "/" + repo
	switch operation {
	case "import":
		if !policy.AllowImport {
			return fmt.Errorf("github import is disabled by project policy")
		}
	case "push":
		if !policy.AllowPush {
			return fmt.Errorf("github push is disabled by project policy")
		}
		if strings.EqualFold(strategy, "commit") && (!policy.AllowDirectCommit || policy.RequirePullRequest) {
			return fmt.Errorf("project policy requires GitHub pull requests")
		}
	}
	if !githubPolicyAllowsPattern(fullRepo, policy.AllowedRepos, true) {
		return fmt.Errorf("repository %s is not allowed by project policy", fullRepo)
	}
	if !githubPolicyAllowsPattern(branch, policy.AllowedBranches, false) {
		return fmt.Errorf("branch %s is not allowed by project policy", branch)
	}
	if operation != "check" {
		if prefix := githubPolicyStaticPrefix(policy.PathTemplate); prefix != "" {
			normalized := strings.Trim(filePath, "/")
			if normalized != prefix && !strings.HasPrefix(normalized, strings.TrimSuffix(prefix, "/")+"/") {
				return fmt.Errorf("path %s is outside the project GitHub path policy", filePath)
			}
		}
	}
	return nil
}

func (s *Server) githubClientForProjectRequest(w http.ResponseWriter, r *http.Request, projectID, owner, repo string) (*githubResolvedClient, bool) {
	settings := s.currentGitHubSettings()
	if !s.githubFeatureGuard(w, settings) {
		return nil, false
	}
	policy := s.githubProjectPolicy(projectID)
	mode := strings.ToLower(strings.TrimSpace(settings.AutomationMode))
	if mode == "" {
		mode = defaultGitHubAutoMode
	}
	// App installation tokens and PATs are controller-owned credentials. They
	// are available to project requests only when an administrator has enabled
	// the project's repository/branch policy; otherwise a project editor could
	// turn a broad controller credential into arbitrary GitHub access. Per-user
	// OAuth remains available without a project policy because its authority is
	// already bounded by the caller's own GitHub identity.
	controllerCredentialAllowed := policy.Enabled
	tryApp := controllerCredentialAllowed && (mode == defaultGitHubAutoMode || mode == "app")
	tryOAuth := mode == defaultGitHubAutoMode || mode == "oauth"
	tryPAT := controllerCredentialAllowed && (mode == defaultGitHubAutoMode || mode == "pat")

	if tryApp && settings.appConfigured() && policy.AppInstallationID > 0 {
		if client, err := s.githubInstallationClient(r.Context(), settings, policy.AppInstallationID, owner, repo); err == nil {
			return &githubResolvedClient{Client: client, Settings: settings, Policy: policy, AuthMode: "app"}, true
		}
	}
	if tryOAuth && settings.oauthConfigured() {
		store, ok := s.githubConnectionStore()
		if ok {
			if conn, err := store.GetGitHubConnection(githubUserKey(r)); err == nil && conn != nil {
				if token, ok := s.githubTokenForConnection(r.Context(), settings, conn, store); ok {
					if client, err := newGitHubClient(settings, token, githubClientTimeout); err == nil {
						return &githubResolvedClient{Client: client, Settings: settings, Policy: policy, AuthMode: "oauth"}, true
					}
				}
			}
		}
	}
	if tryPAT && settings.patConfigured() {
		if client, err := newGitHubClient(settings, settings.patToken(), githubClientTimeout); err == nil {
			return &githubResolvedClient{Client: client, Settings: settings, Policy: policy, AuthMode: "pat"}, true
		}
	}
	writeError(w, http.StatusUnauthorized, "no usable github automation, oauth, or PAT credential is available")
	return nil, false
}

func (s *Server) githubTokenForConnection(ctx context.Context, settings GitHubSettings, conn *storage.GitHubConnection, store storage.GitHubConnectionStore) (string, bool) {
	token, err := decryptGitHubToken(conn.AccessTokenEnc)
	if err != nil || strings.TrimSpace(token) == "" {
		return "", false
	}
	if conn.ExpiresAt == nil || strings.TrimSpace(conn.RefreshTokenEnc) == "" || time.Now().UTC().Before(conn.ExpiresAt.Add(-5*time.Minute)) {
		return token, true
	}
	refresh, err := decryptGitHubToken(conn.RefreshTokenEnc)
	if err != nil || strings.TrimSpace(refresh) == "" {
		return token, true
	}
	next, err := refreshGitHubOAuthToken(ctx, settings, refresh)
	if err != nil {
		return token, true
	}
	expectedUpdatedAt := conn.UpdatedAt
	encAccess, err := encryptGitHubToken(next.AccessToken)
	if err != nil {
		return token, true
	}
	conn.AccessTokenEnc = encAccess
	if next.RefreshToken != "" {
		if encRefresh, err := encryptGitHubToken(next.RefreshToken); err == nil {
			conn.RefreshTokenEnc = encRefresh
		}
	}
	conn.TokenType = firstNonEmpty(next.TokenType, conn.TokenType)
	conn.Scopes = firstNonEmpty(next.Scope, conn.Scopes)
	if next.ExpiresIn > 0 {
		exp := time.Now().UTC().Add(time.Duration(next.ExpiresIn) * time.Second)
		conn.ExpiresAt = &exp
	}
	updater, supported := store.(interface {
		UpdateGitHubConnectionIfUnchanged(conn *storage.GitHubConnection, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		slog.Warn("refusing refreshed GitHub OAuth token because storage lacks disconnect-safe compare-and-swap", "user_id", conn.UserID)
		return "", false
	}
	updated, err := updater.UpdateGitHubConnectionIfUnchanged(conn, expectedUpdatedAt)
	if err != nil {
		slog.Warn("failed to persist refreshed GitHub OAuth token", "user_id", conn.UserID, "error", err)
		return "", false
	}
	if !updated {
		// A concurrent refresh may have won, or the user may have disconnected.
		// Reloading distinguishes the safe winner from deletion without ever
		// resurrecting the old row.
		current, err := store.GetGitHubConnection(conn.UserID)
		if err != nil || current == nil {
			return "", false
		}
		currentToken, err := decryptGitHubToken(current.AccessTokenEnc)
		if err != nil || strings.TrimSpace(currentToken) == "" {
			return "", false
		}
		return currentToken, true
	}
	return next.AccessToken, true
}

func refreshGitHubOAuthToken(ctx context.Context, settings GitHubSettings, refreshToken string) (*githubTokenResponse, error) {
	endpoint := settings.WebBaseURL + "/login/oauth/access_token"
	if err := validateOutboundURLAllowingHosts(endpoint, githubAllowSet(settings)); err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("client_id", settings.ClientID)
	form.Set("client_secret", settings.clientSecret())
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
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
		return nil, err
	}
	defer resp.Body.Close()
	var tokenResp githubTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tokenResp); err != nil {
		return nil, err
	}
	if tokenResp.Error != "" || strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, fmt.Errorf("github refresh token exchange failed")
	}
	return &tokenResp, nil
}

func (s *Server) handleGitHubDiagnostics(w http.ResponseWriter, r *http.Request) {
	settings := s.currentGitHubSettings()
	out := map[string]any{
		"enabled":              settings.Enabled,
		"oauth_configured":     settings.oauthConfigured(),
		"app_configured":       settings.appConfigured(),
		"webhook_configured":   settings.webhookConfigured(),
		"pat_configured":       settings.patConfigured(),
		"encryption_available": githubTokenEncryptionAvailable(),
		"automation_mode":      settings.AutomationMode,
		"checks_enabled":       settings.ChecksEnabled,
		"api_base_url":         settings.APIBaseURL,
		"web_base_url":         settings.WebBaseURL,
	}
	if client, err := newGitHubClient(settings, "", 10*time.Second); err == nil && settings.Enabled {
		if meta, err := client.Meta(r.Context()); err == nil {
			out["ghes_version"] = meta.InstalledVersion
			out["reachable"] = true
		} else {
			out["reachable"] = false
			out["reachability_error"] = err.Error()
		}
	}
	if store, ok := s.githubEnterpriseStore(); ok {
		if installations, err := store.ListGitHubAppInstallations(); err == nil {
			out["known_installations"] = len(installations)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	settings := s.currentGitHubSettings()
	store, ok := s.githubEnterpriseStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github enterprise storage is unavailable")
		return
	}
	if strings.EqualFold(r.URL.Query().Get("refresh"), "true") {
		if !settings.appConfigured() {
			writeError(w, http.StatusServiceUnavailable, "github app is not configured")
			return
		}
		client, err := s.githubAppClient(r.Context(), settings)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		installations, err := client.ListAppInstallations(r.Context())
		if err != nil {
			writeGitHubUpstreamError(w, err)
			return
		}
		for _, inst := range installations {
			perms, _ := json.Marshal(inst.Permissions)
			events, _ := json.Marshal(inst.Events)
			if err := store.SaveGitHubAppInstallation(&storage.GitHubAppInstallation{
				InstallationID:      inst.ID,
				AccountLogin:        inst.Account.Login,
				AccountType:         inst.Account.Type,
				RepositorySelection: inst.RepositorySelection,
				PermissionsJSON:     string(perms),
				EventsJSON:          string(events),
				HTMLURL:             inst.HTMLURL,
				SuspendedAt:         inst.SuspendedAt,
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to save github installation")
				return
			}
		}
	}
	installations, err := store.ListGitHubAppInstallations()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list github installations")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"installations": installations, "count": len(installations)})
}

func (s *Server) handleGetProjectGitHubPolicy(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	writeJSON(w, http.StatusOK, normalizeGitHubProjectPolicy(project.Settings.GitHub))
}

func (s *Server) handleUpdateProjectGitHubPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.requireSystemAdminForGitHubPolicyMutation(w, r) {
		return
	}
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	var policy storage.GitHubProjectPolicy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&policy); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	project.Settings.GitHub = normalizeGitHubProjectPolicy(policy)
	if err := validateProjectSettings(project.Settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updater, supported := s.store.(interface {
		UpdateProjectIfUnchanged(project *storage.Project, expectedUpdatedAt time.Time) (bool, error)
	})
	if !supported {
		writeError(w, http.StatusNotImplemented, "optimistic project policy updates are not supported by this storage backend")
		return
	}
	updated, err := updater.UpdateProjectIfUnchanged(project, project.UpdatedAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save project policy")
		return
	}
	if !updated {
		writeError(w, http.StatusConflict, "project changed while the policy update was in flight; reload and retry")
		return
	}
	s.recordAuditEvent(r, "github.project_policy.update", "success", "project", project.ID, project.ID, map[string]any{"github_policy_enabled": project.Settings.GitHub.Enabled})
	writeJSON(w, http.StatusOK, project.Settings.GitHub)
}

type githubSyncJobRequest struct {
	ProjectID string   `json:"project_id"`
	Kind      string   `json:"kind"`
	Owner     string   `json:"owner"`
	Repo      string   `json:"repo"`
	Branch    string   `json:"branch"`
	Path      string   `json:"path"`
	ScriptIDs []string `json:"script_ids"`
	Strategy  string   `json:"strategy"`
}

func (s *Server) handleCreateGitHubSyncJob(w http.ResponseWriter, r *http.Request) {
	var req githubSyncJobRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.ProjectID = normalizeProjectID(req.ProjectID)
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	if req.Kind == "" {
		req.Kind = "import"
	}
	if req.Kind != "import" && req.Kind != "push" {
		writeError(w, http.StatusBadRequest, "kind must be import or push")
		return
	}
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	policy := s.githubProjectPolicy(req.ProjectID)
	settings := s.currentGitHubSettings()
	if repo == "" {
		if defOwner, defRepo, ok := splitOwnerRepo(firstNonEmpty(policy.DefaultRepo, settings.DefaultRepo)); ok {
			owner, repo = defOwner, defRepo
		}
	}
	branch := firstNonEmpty(strings.TrimSpace(req.Branch), policy.DefaultBranch, settings.DefaultBranch)
	basePath := strings.Trim(strings.TrimSpace(firstNonEmpty(req.Path, policy.DefaultSyncDirectory)), "/")
	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo are required")
		return
	}
	resolved, ok := s.githubClientForProjectRequest(w, r, req.ProjectID, owner, repo)
	if !ok {
		return
	}
	if resolved.Policy.Enabled && !resolved.Policy.BulkSyncEnabled {
		writeError(w, http.StatusForbidden, "github bulk sync is disabled by project policy")
		return
	}
	if err := enforceGitHubProjectPolicy(resolved.Policy, req.Kind, owner, repo, branch, basePath, req.Strategy); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	store, _ := s.githubEnterpriseStore()
	linkStore, _ := s.githubScriptLinkStore()
	syncService := newGitHubSyncService(s.runner, store, linkStore)
	job, err := syncService.Run(r.Context(), resolved, githubSyncJobInput{
		ProjectID: req.ProjectID,
		Kind:      req.Kind,
		Owner:     owner,
		Repo:      repo,
		Branch:    branch,
		Path:      basePath,
		ScriptIDs: req.ScriptIDs,
		Strategy:  req.Strategy,
		StartedBy: githubUserKey(r),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.recordAuditEvent(r, "github.sync_job.create", job.Status, "github_sync_job", job.ID, req.ProjectID, map[string]any{"kind": job.Kind, "owner": owner, "repo": repo, "branch": branch})
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleListGitHubSyncJobs(w http.ResponseWriter, r *http.Request) {
	store, ok := s.githubEnterpriseStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github enterprise storage is unavailable")
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = authorizedProjectID
	}
	jobs, err := store.ListGitHubSyncJobs(projectID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list github sync jobs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "count": len(jobs)})
}

func (s *Server) handleGetGitHubSyncJob(w http.ResponseWriter, r *http.Request) {
	store, ok := s.githubEnterpriseStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github enterprise storage is unavailable")
		return
	}
	job, err := store.GetGitHubSyncJob(r.PathValue("id"))
	if err != nil || job == nil {
		writeError(w, http.StatusNotFound, "github sync job not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, job.ProjectID) {
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func verifyGitHubWebhookSignature(secret string, payload []byte, header string) bool {
	if strings.TrimSpace(secret) == "" || strings.TrimSpace(header) == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(header))
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	settings := s.currentGitHubSettings()
	if !settings.Enabled || !settings.webhookConfigured() {
		writeError(w, http.StatusServiceUnavailable, "github webhook is not configured")
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook body")
		return
	}
	if !verifyGitHubWebhookSignature(settings.webhookSecret(), payload, r.Header.Get("X-Hub-Signature-256")) {
		writeError(w, http.StatusUnauthorized, "github webhook signature mismatch")
		return
	}
	deliveryID := strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	if deliveryID == "" {
		deliveryID = uuid.New().String()
	}
	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	store, ok := s.githubEnterpriseStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github enterprise storage is unavailable")
		return
	}
	if existing, _ := store.GetGitHubWebhookDelivery(deliveryID); existing != nil {
		s.recordAuditEvent(r, "github.webhook.receive", "deduped", "github_webhook_delivery", deliveryID, "", map[string]any{"event": event})
		writeJSON(w, http.StatusOK, map[string]any{"deduped": true, "delivery_id": deliveryID})
		return
	}
	delivery := &storage.GitHubWebhookDelivery{DeliveryID: deliveryID, Event: event, Status: "received"}
	var raw map[string]any
	_ = json.Unmarshal(payload, &raw)
	delivery.Action, _ = raw["action"].(string)
	if repo := mapFromAny(raw["repository"]); len(repo) > 0 {
		delivery.Repo = stringValue(repo["name"])
		if owner := mapFromAny(repo["owner"]); len(owner) > 0 {
			delivery.Owner = stringValue(owner["login"])
		}
	}
	delivery.Ref = stringValue(raw["ref"])
	if head := stringValue(raw["after"]); head != "" {
		delivery.CommitSHA = head
	}
	processed := time.Now()
	delivery.ProcessedAt = &processed
	delivery.Status = "processed"
	linksUpdated := 0
	if event == "push" && delivery.Owner != "" && delivery.Repo != "" {
		linksUpdated, err = s.updateGitHubLinksFromWebhook(delivery.Owner, delivery.Repo, delivery.Ref, delivery.CommitSHA)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update github links")
			return
		}
	}
	delivery.SummaryJSON = fmt.Sprintf(`{"links_updated":%d}`, linksUpdated)
	if err := store.SaveGitHubWebhookDelivery(delivery); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save github webhook delivery")
		return
	}
	s.recordAuditEvent(r, "github.webhook.receive", delivery.Status, "github_webhook_delivery", deliveryID, "", map[string]any{
		"event":         event,
		"owner":         delivery.Owner,
		"repo":          delivery.Repo,
		"ref":           delivery.Ref,
		"commit_sha":    delivery.CommitSHA,
		"links_updated": linksUpdated,
	})
	writeJSON(w, http.StatusOK, map[string]any{"delivery_id": deliveryID, "status": delivery.Status, "links_updated": linksUpdated})
}

type githubScriptLinkRemoteCommitStore interface {
	UpdateGitHubScriptLinksRemoteCommit(owner, repo, branch, commitSHA string, updatedAt time.Time) (int64, error)
}

func (s *Server) updateGitHubLinksFromWebhook(owner, repo, ref, commitSHA string) (int, error) {
	if _, ok := s.githubScriptLinkStore(); !ok {
		return 0, nil
	}
	store, ok := s.store.(githubScriptLinkRemoteCommitStore)
	if !ok {
		return 0, fmt.Errorf("github link storage does not support atomic remote-commit updates")
	}
	branch := strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	updated, err := store.UpdateGitHubScriptLinksRemoteCommit(
		strings.TrimSpace(owner), strings.TrimSpace(repo), branch, strings.TrimSpace(commitSHA), time.Now().UTC(),
	)
	return int(updated), err
}

type githubCheckPublishRequest struct {
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	CommitSHA  string `json:"commit_sha"`
	ScriptID   string `json:"script_id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
	Summary    string `json:"summary"`
}

func (s *Server) handlePublishGitHubCheckRun(w http.ResponseWriter, r *http.Request) {
	testID := r.PathValue("id")
	test, ok := s.runner.GetTest(testID)
	if !ok {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, test.ProjectID) {
		return
	}
	var req githubCheckPublishRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	scriptID := firstNonEmpty(req.ScriptID, test.ScriptID)
	if scriptID == "" && len(test.Groups) > 0 {
		scriptID = test.Groups[0].ScriptID
	}
	if scriptID != "" {
		scriptProjectID, found := s.githubScriptProjectID(scriptID)
		if !found {
			writeError(w, http.StatusBadRequest, "script not found")
			return
		}
		if normalizeProjectID(scriptProjectID) != normalizeProjectID(test.ProjectID) {
			writeError(w, http.StatusForbidden, "script does not belong to the test project")
			return
		}
	}
	linkStore, _ := s.githubScriptLinkStore()
	if linkStore != nil && scriptID != "" {
		if link, _ := linkStore.GetGitHubScriptLink(scriptID); link != nil {
			req.Owner = firstNonEmpty(req.Owner, link.Owner)
			req.Repo = firstNonEmpty(req.Repo, link.Repo)
			req.Branch = firstNonEmpty(req.Branch, link.Branch)
			req.CommitSHA = firstNonEmpty(req.CommitSHA, link.RemoteCommitSHA)
		}
	}
	if req.Owner == "" || req.Repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo are required")
		return
	}
	policy := s.githubProjectPolicy(test.ProjectID)
	if err := enforceGitHubProjectPolicy(policy, "check", req.Owner, req.Repo, req.Branch, "", ""); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	resolved, ok := s.githubClientForProjectRequest(w, r, test.ProjectID, req.Owner, req.Repo)
	if !ok {
		return
	}
	if req.CommitSHA == "" && req.Branch != "" {
		if head, err := resolved.Client.GetBranchHeadSHA(r.Context(), req.Owner, req.Repo, req.Branch); err == nil {
			req.CommitSHA = head
		}
	}
	if req.CommitSHA == "" {
		writeError(w, http.StatusBadRequest, "commit_sha is required; linked blob shas cannot be used for check runs")
		return
	}
	if !resolved.Settings.ChecksEnabled && !resolved.Policy.ChecksEnabled {
		writeError(w, http.StatusBadRequest, "github check publishing is disabled")
		return
	}
	name := firstNonEmpty(req.Name, "APEX Load Performance Gate")
	conclusion := firstNonEmpty(req.Conclusion, githubConclusionForTestStatus(test.Status))
	summary := firstNonEmpty(req.Summary, "APEX Load run "+test.Name+" finished with status "+test.Status+".")
	check, err := resolved.Client.CreateCheckRun(r.Context(), req.Owner, req.Repo, name, req.CommitSHA, "completed", conclusion, summary, req.DetailsURL)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	checkID := uuid.New().String()
	if store, ok := s.githubEnterpriseStore(); ok {
		if err := store.SaveGitHubCheckLink(&storage.GitHubCheckLink{
			ID:          checkID,
			TestID:      testID,
			ScriptID:    scriptID,
			Owner:       req.Owner,
			Repo:        req.Repo,
			CommitSHA:   req.CommitSHA,
			CheckRunID:  check.ID,
			CheckRunURL: check.HTMLURL,
			Status:      check.Status,
			Conclusion:  check.Conclusion,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "GitHub check was created but its APEX Load link could not be persisted")
			return
		}
	}
	s.recordAuditEvent(r, "github.check_run.publish", "success", "test", testID, test.ProjectID, map[string]any{"owner": req.Owner, "repo": req.Repo, "commit_sha": req.CommitSHA, "check_run_id": check.ID})
	writeJSON(w, http.StatusOK, map[string]any{"id": checkID, "check_run": check, "auth_mode": resolved.AuthMode})
}

func githubConclusionForTestStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETED":
		return "success"
	case "FAILED", "ABORTED":
		return "failure"
	default:
		return "neutral"
	}
}

func (s *Server) handleListGitHubCheckLinks(w http.ResponseWriter, r *http.Request) {
	store, ok := s.githubEnterpriseStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github enterprise storage is unavailable")
		return
	}
	links, err := store.ListGitHubCheckLinks(r.URL.Query().Get("test_id"), r.URL.Query().Get("script_id"), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list github check links")
		return
	}
	if projectID, scoped := authorizedProjectFromRequest(r); scoped {
		links = s.filterGitHubCheckLinksByProject(links, projectID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"check_links": links, "count": len(links)})
}

func (s *Server) githubScriptProjectID(scriptID string) (string, bool) {
	if s != nil && s.store != nil {
		script, err := s.store.GetScript(strings.TrimSpace(scriptID))
		if err == nil && script != nil {
			return script.ProjectID, true
		}
		return "", false
	}
	if s != nil && s.runner != nil {
		if script, ok := s.runner.GetScript(strings.TrimSpace(scriptID)); ok && script != nil {
			return script.ProjectID, true
		}
	}
	return "", false
}

func (s *Server) filterGitHubCheckLinksByProject(links []*storage.GitHubCheckLink, projectID string) []*storage.GitHubCheckLink {
	projectID = normalizeProjectID(projectID)
	visible := make([]*storage.GitHubCheckLink, 0, len(links))
	for _, link := range links {
		if link == nil {
			continue
		}
		test, err := s.store.GetTest(strings.TrimSpace(link.TestID))
		if err != nil || test == nil || normalizeProjectID(test.ProjectID) != projectID {
			continue
		}
		if strings.TrimSpace(link.ScriptID) != "" {
			script, err := s.store.GetScript(strings.TrimSpace(link.ScriptID))
			if err != nil || script == nil || normalizeProjectID(script.ProjectID) != projectID {
				continue
			}
		}
		visible = append(visible, link)
	}
	return visible
}

func (s *Server) handleListGitHubWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	store, ok := s.githubEnterpriseStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github enterprise storage is unavailable")
		return
	}
	deliveries, err := store.ListGitHubWebhookDeliveries(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list github webhook deliveries")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deliveries": deliveries, "count": len(deliveries)})
}

func githubJSONSummary(value any) string {
	data, _ := json.Marshal(value)
	return string(bytes.TrimSpace(data))
}
