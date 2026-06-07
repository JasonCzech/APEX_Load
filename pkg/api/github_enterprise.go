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
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const githubInstallationTokenSkew = 5 * time.Minute

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
	s.githubMu.Lock()
	if cached, ok := s.githubTokens[cacheKey]; ok && cached.Token != "" && time.Now().UTC().Before(cached.ExpiresAt.Add(-githubInstallationTokenSkew)) {
		s.githubMu.Unlock()
		return newGitHubClient(settings, cached.Token, githubClientTimeout)
	}
	s.githubMu.Unlock()

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
	s.githubMu.Lock()
	s.githubTokens[cacheKey] = githubCachedToken{Token: token.Token, ExpiresAt: token.ExpiresAt, InstallationID: installationID, Repo: owner + "/" + repo}
	s.githubMu.Unlock()
	return newGitHubClient(settings, token.Token, githubClientTimeout)
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
	if prefix := githubPolicyStaticPrefix(policy.PathTemplate); prefix != "" {
		normalized := strings.Trim(filePath, "/")
		if normalized != prefix && !strings.HasPrefix(normalized, strings.TrimSuffix(prefix, "/")+"/") {
			return fmt.Errorf("path %s is outside the project GitHub path policy", filePath)
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
	tryApp := mode == defaultGitHubAutoMode || mode == "app"
	tryOAuth := mode == defaultGitHubAutoMode || mode == "oauth"
	tryPAT := mode == defaultGitHubAutoMode || mode == "pat"

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
	_ = store.SaveGitHubConnection(conn)
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
	if err := s.store.SaveProject(project); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save project policy")
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
	job := &storage.GitHubSyncJob{
		ID:        uuid.New().String(),
		ProjectID: req.ProjectID,
		Kind:      req.Kind,
		Status:    "running",
		Owner:     owner,
		Repo:      repo,
		Branch:    branch,
		Path:      basePath,
		StartedBy: githubUserKey(r),
	}
	store, _ := s.githubEnterpriseStore()
	if store != nil {
		_ = store.SaveGitHubSyncJob(job)
	}
	switch req.Kind {
	case "import":
		s.runGitHubImportSyncJob(r.Context(), resolved.Client, job)
	case "push":
		s.runGitHubPushSyncJob(r.Context(), resolved, job, req.ScriptIDs, req.Strategy)
	default:
		writeError(w, http.StatusBadRequest, "kind must be import or push")
		return
	}
	job.Status = "complete"
	completed := time.Now()
	job.CompletedAt = &completed
	if job.Errors > 0 || job.Conflicts > 0 {
		job.Status = "partial"
	}
	if store != nil {
		_ = store.SaveGitHubSyncJob(job)
	}
	s.recordAuditEvent(r, "github.sync_job.create", job.Status, "github_sync_job", job.ID, req.ProjectID, map[string]any{"kind": job.Kind, "owner": owner, "repo": repo, "branch": branch})
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) runGitHubImportSyncJob(ctx context.Context, client *githubClient, job *storage.GitHubSyncJob) {
	contents, err := client.GetContents(ctx, job.Owner, job.Repo, job.Path, job.Branch)
	if err != nil {
		job.Errors++
		job.SummaryJSON = fmt.Sprintf(`{"error":%q}`, err.Error())
		return
	}
	remoteCommitSHA, _ := client.GetBranchHeadSHA(ctx, job.Owner, job.Repo, job.Branch)
	entries := contents.Entries
	if !contents.IsDir && contents.File != nil {
		entries = []githubContent{*contents.File}
	}
	job.Total = len(entries)
	for _, entry := range entries {
		if entry.Type != "file" || !strings.HasSuffix(strings.ToLower(entry.Path), ".json") {
			continue
		}
		fileContents, err := client.GetContents(ctx, job.Owner, job.Repo, entry.Path, job.Branch)
		if err != nil || fileContents.File == nil {
			job.Errors++
			continue
		}
		data, err := fileContents.File.DecodedContent()
		if err != nil {
			job.Errors++
			continue
		}
		script, err := dsl.Parse(data)
		if err != nil {
			job.Errors++
			continue
		}
		script.ProjectID = job.ProjectID
		stored := s.runner.StoreScript(script)
		job.Created++
		job.Processed++
		if linkStore, ok := s.githubScriptLinkStore(); ok {
			_, contentHash, _ := githubScriptEnvelope(stored.Script)
			_ = linkStore.SaveGitHubScriptLink(&storage.GitHubScriptLink{
				ScriptID:        stored.ID,
				RefKey:          stored.RefKey,
				Owner:           job.Owner,
				Repo:            job.Repo,
				Branch:          job.Branch,
				Path:            entry.Path,
				LastCommitSHA:   fileContents.File.SHA,
				RemoteBlobSHA:   fileContents.File.SHA,
				RemoteCommitSHA: remoteCommitSHA,
				ContentHash:     contentHash,
			})
		}
	}
}

func (s *Server) runGitHubPushSyncJob(ctx context.Context, resolved *githubResolvedClient, job *storage.GitHubSyncJob, scriptIDs []string, strategy string) {
	if strategy == "" {
		strategy = "pr"
	}
	selected := map[string]struct{}{}
	for _, id := range scriptIDs {
		selected[id] = struct{}{}
	}
	scripts := s.runner.ListScriptsFiltered(job.ProjectID, nil)
	job.Total = len(scripts)
	for _, stored := range scripts {
		if len(selected) > 0 {
			if _, ok := selected[stored.ID]; !ok {
				continue
			}
		}
		targetPath := renderGitHubPathTemplate(firstNonEmpty(resolved.Policy.PathTemplate, resolved.Settings.PathTemplate), stored)
		if err := enforceGitHubProjectPolicy(resolved.Policy, "push", job.Owner, job.Repo, job.Branch, targetPath, strategy); err != nil {
			job.Conflicts++
			continue
		}
		content, contentHash, err := githubScriptEnvelope(stored.Script)
		if err != nil {
			job.Errors++
			continue
		}
		existing, _ := resolved.Client.GetContents(ctx, job.Owner, job.Repo, targetPath, job.Branch)
		remoteSHA := ""
		if existing != nil && existing.File != nil {
			remoteSHA = existing.File.SHA
		}
		result, err := resolved.Client.PutFile(ctx, job.Owner, job.Repo, targetPath, job.Branch, "Sync "+stored.Name+" via APEX Load", content, remoteSHA)
		if err != nil {
			job.Errors++
			continue
		}
		if linkStore, ok := s.githubScriptLinkStore(); ok {
			_ = linkStore.SaveGitHubScriptLink(&storage.GitHubScriptLink{
				ScriptID:        stored.ID,
				RefKey:          stored.RefKey,
				Owner:           job.Owner,
				Repo:            job.Repo,
				Branch:          job.Branch,
				Path:            targetPath,
				LastCommitSHA:   result.Content.SHA,
				RemoteBlobSHA:   result.Content.SHA,
				RemoteCommitSHA: result.Commit.SHA,
				ContentHash:     contentHash,
			})
		}
		job.Updated++
		job.Processed++
	}
}

func (s *Server) handleListGitHubSyncJobs(w http.ResponseWriter, r *http.Request) {
	store, ok := s.githubEnterpriseStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github enterprise storage is unavailable")
		return
	}
	jobs, err := store.ListGitHubSyncJobs(normalizeProjectID(r.URL.Query().Get("project_id")), 50)
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
		linksUpdated = s.updateGitHubLinksFromWebhook(delivery.Owner, delivery.Repo, delivery.Ref, delivery.CommitSHA)
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

func (s *Server) updateGitHubLinksFromWebhook(owner, repo, ref, commitSHA string) int {
	// The existing link store is keyed by script id and has no list method; update
	// visible script statuses opportunistically by checking every local script.
	linkStore, ok := s.githubScriptLinkStore()
	if !ok {
		return 0
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	count := 0
	for _, script := range s.runner.ListScripts() {
		link, err := linkStore.GetGitHubScriptLink(script.ID)
		if err != nil || link == nil {
			continue
		}
		if strings.EqualFold(link.Owner, owner) && strings.EqualFold(link.Repo, repo) && (branch == "" || link.Branch == branch) {
			link.RemoteCommitSHA = commitSHA
			if err := linkStore.SaveGitHubScriptLink(link); err == nil {
				count++
			}
		}
	}
	return count
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
	var req githubCheckPublishRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	scriptID := firstNonEmpty(req.ScriptID, test.ScriptID)
	if scriptID == "" && len(test.Groups) > 0 {
		scriptID = test.Groups[0].ScriptID
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
		_ = store.SaveGitHubCheckLink(&storage.GitHubCheckLink{
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
		})
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
	writeJSON(w, http.StatusOK, map[string]any{"check_links": links, "count": len(links)})
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
