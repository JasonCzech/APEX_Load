package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

const githubClientTimeout = 30 * time.Second

// githubClientForRequest resolves the per-user GitHub client for the current
// request: it enforces the feature guard, loads the caller's stored connection,
// decrypts the access token, and builds a host-allowlisted client. On any
// failure it writes the appropriate error response and returns ok=false.
func (s *Server) githubClientForRequest(w http.ResponseWriter, r *http.Request) (*githubClient, GitHubSettings, bool) {
	settings := s.currentGitHubSettings()
	if !s.githubFeatureGuard(w, settings) {
		return nil, settings, false
	}
	store, _ := s.githubConnectionStore()
	conn, err := store.GetGitHubConnection(githubUserKey(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read github connection")
		return nil, settings, false
	}
	if conn == nil {
		writeError(w, http.StatusUnauthorized, "github account is not connected")
		return nil, settings, false
	}
	token, err := decryptGitHubToken(conn.AccessTokenEnc)
	if err != nil || strings.TrimSpace(token) == "" {
		writeError(w, http.StatusServiceUnavailable, "stored github token could not be read")
		return nil, settings, false
	}
	client, err := newGitHubClient(settings, token, githubClientTimeout)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return nil, settings, false
	}
	return client, settings, true
}

// writeGitHubUpstreamError maps a GitHub API error to an HTTP response, passing
// through 401/403/404 so callers can react (e.g. re-auth) while collapsing other
// upstream failures into 502.
func writeGitHubUpstreamError(w http.ResponseWriter, err error) {
	if apiErr, ok := err.(*githubAPIError); ok {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			writeError(w, http.StatusUnauthorized, "github rejected the stored token; reconnect your account")
			return
		case http.StatusForbidden:
			writeError(w, http.StatusForbidden, "github denied access to this resource")
			return
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "github resource not found")
			return
		}
	}
	writeError(w, http.StatusBadGateway, "github request failed")
}

func (s *Server) handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	repos, err := client.ListRepos(r.Context())
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(repos))
	for _, repo := range repos {
		out = append(out, map[string]any{
			"name":           repo.Name,
			"full_name":      repo.FullName,
			"owner":          repo.Owner.Login,
			"private":        repo.Private,
			"default_branch": repo.DefaultBranch,
			"html_url":       repo.HTMLURL,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": out})
}

func (s *Server) handleGitHubBranches(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}
	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	branches, err := client.ListBranches(r.Context(), owner, repo)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(branches))
	for _, branch := range branches {
		out = append(out, map[string]any{"name": branch.Name, "commit_sha": branch.Commit.SHA})
	}
	writeJSON(w, http.StatusOK, map[string]any{"branches": out})
}

func (s *Server) handleGitHubContents(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo query parameters are required")
		return
	}
	contentPath := strings.TrimSpace(r.URL.Query().Get("path"))
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	contents, err := client.GetContents(r.Context(), owner, repo, contentPath, ref)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	if contents.IsDir {
		entries := make([]map[string]any, 0, len(contents.Entries))
		for _, entry := range contents.Entries {
			entries = append(entries, map[string]any{
				"type": entry.Type,
				"name": entry.Name,
				"path": entry.Path,
				"sha":  entry.SHA,
				"size": entry.Size,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"type": "dir", "path": contentPath, "entries": entries})
		return
	}
	file := contents.File
	writeJSON(w, http.StatusOK, map[string]any{
		"type":     "file",
		"name":     file.Name,
		"path":     file.Path,
		"sha":      file.SHA,
		"size":     file.Size,
		"html_url": file.HTMLURL,
	})
}

type githubImportRequest struct {
	ProjectID string `json:"project_id"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Ref       string `json:"ref,omitempty"`
	Name      string `json:"name,omitempty"`
}

// handleImportGitHub pulls a single script file from a GitHub repository, parses
// and validates it as DSL, and returns a standard import-review response (no save
// — the dashboard saves through the existing create/update flow after review).
func (s *Server) handleImportGitHub(w http.ResponseWriter, r *http.Request) {
	var req githubImportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Owner = strings.TrimSpace(req.Owner)
	req.Repo = strings.TrimSpace(req.Repo)
	req.Path = strings.TrimSpace(req.Path)
	if req.Owner == "" || req.Repo == "" || req.Path == "" {
		writeError(w, http.StatusBadRequest, "owner, repo, and path are required")
		return
	}

	client, _, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	contents, err := client.GetContents(r.Context(), req.Owner, req.Repo, req.Path, req.Ref)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	if contents.IsDir || contents.File == nil {
		writeError(w, http.StatusBadRequest, "the selected github path is a directory, not a script file")
		return
	}
	data, err := contents.File.DecodedContent()
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "failed to decode github file content")
		return
	}

	script, err := dsl.Parse(data)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "github file is not a valid script: "+err.Error())
		return
	}
	script.ProjectID = normalizeProjectID(req.ProjectID)

	sourceName := strings.TrimSpace(req.Name)
	if sourceName == "" {
		sourceName = strings.TrimSuffix(path.Base(req.Path), path.Ext(req.Path))
	}
	branch := strings.TrimSpace(req.Ref)
	if branch == "" {
		branch = "(default)"
	}
	review := map[string]any{
		"owner":      req.Owner,
		"repo":       req.Repo,
		"branch":     branch,
		"path":       contents.File.Path,
		"commit_sha": contents.File.SHA,
		"html_url":   contents.File.HTMLURL,
	}
	attachSourceReviewToScript(script, "github", sourceName, review)

	var warnings []string
	if issues := validateScript(script); len(issues) > 0 {
		warnings = append(warnings, issues...)
	}

	s.recordAuditEvent(r, "github.script.import", "success", "script", script.Name, script.ProjectID, map[string]any{
		"owner": req.Owner, "repo": req.Repo, "path": contents.File.Path,
	})
	writeJSON(w, http.StatusOK, newScriptImportResponse(scriptImportResponseOptions{
		Script:   script,
		Warnings: warnings,
		Summary: map[string]any{
			"owner":      req.Owner,
			"repo":       req.Repo,
			"branch":     branch,
			"path":       contents.File.Path,
			"commit_sha": contents.File.SHA,
		},
		ReviewSections: []scriptImportReviewSection{{
			Key:      "github_source",
			Title:    "GitHub Source",
			Severity: "info",
			Summary:  "Imported from a GitHub repository. Review and save to add it to your library.",
			Items: []string{
				req.Owner + "/" + req.Repo + " @ " + branch,
				contents.File.Path,
				"commit " + shortGitHubSHA(contents.File.SHA),
			},
		}},
		SourceReview: review,
	}))
}

func shortGitHubSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func (s *Server) githubScriptLinkStore() (storage.GitHubScriptLinkStore, bool) {
	store, ok := s.store.(storage.GitHubScriptLinkStore)
	return store, ok
}

// githubScriptEnvelope serializes a stored script in the canonical
// {"script": …} envelope (matching the download/export handlers) and returns the
// bytes plus a stable content hash for drift detection.
func githubScriptEnvelope(script *dsl.Script) ([]byte, string, error) {
	data, err := json.MarshalIndent(map[string]any{"script": script}, "", "  ")
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// renderGitHubPathTemplate fills the configured path template for a script.
func renderGitHubPathTemplate(tmpl string, stored *StoredScript) string {
	replacer := strings.NewReplacer(
		"{project}", normalizeProjectID(stored.ProjectID),
		"{ref_key}", firstNonEmpty(stored.RefKey, stored.ID),
		"{id}", stored.ID,
		"{name}", sanitizeFilename(stored.Name),
	)
	return strings.TrimPrefix(replacer.Replace(tmpl), "/")
}

// githubFileWebURL builds a "View on GitHub" blob deep link from the global web
// base URL (works without a per-user connection).
func githubFileWebURL(settings GitHubSettings, owner, repo, branch, filePath string) string {
	if owner == "" || repo == "" || branch == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s/blob/%s/%s", strings.TrimRight(settings.WebBaseURL, "/"), owner, repo, branch, strings.TrimPrefix(filePath, "/"))
}

type githubPushRequest struct {
	Owner    string `json:"owner"`
	Repo     string `json:"repo"`
	Branch   string `json:"branch,omitempty"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message,omitempty"`
	Strategy string `json:"strategy,omitempty"` // "commit" (default) | "pr"
	PRBranch string `json:"pr_branch,omitempty"`
	PRTitle  string `json:"pr_title,omitempty"`
	PRBody   string `json:"pr_body,omitempty"`
}

// handlePushScriptGitHub pushes a single script to GitHub as one JSON file,
// either as a direct commit on the target branch or via a pull request. The
// local↔remote link is upserted so subsequent pushes are idempotent (optimistic
// concurrency via the stored blob sha) and drift can be detected.
func (s *Server) handlePushScriptGitHub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	var req githubPushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	client, settings, ok := s.githubClientForRequest(w, r)
	if !ok {
		return
	}
	linkStore, ok := s.githubScriptLinkStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github link storage is unavailable")
		return
	}

	// Resolve target coordinates, falling back to global defaults.
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	if repo == "" {
		// Allow owner/repo packed in DefaultRepo ("owner/name").
		if defOwner, defRepo, ok := splitOwnerRepo(settings.DefaultRepo); ok {
			if owner == "" {
				owner = defOwner
			}
			repo = defRepo
		}
	}
	if owner == "" || repo == "" {
		writeError(w, http.StatusBadRequest, "owner and repo are required")
		return
	}
	branch := firstNonEmpty(strings.TrimSpace(req.Branch), settings.DefaultBranch)
	targetPath := firstNonEmpty(strings.TrimSpace(req.Path), renderGitHubPathTemplate(settings.PathTemplate, stored))
	if targetPath == "" {
		writeError(w, http.StatusBadRequest, "a target path is required")
		return
	}
	message := firstNonEmpty(strings.TrimSpace(req.Message), fmt.Sprintf("Update %s via APEX Load", stored.Name))

	content, contentHash, err := githubScriptEnvelope(stored.Script)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialize script")
		return
	}

	// Determine the current remote blob sha (for updates / conflict detection).
	existing, err := client.GetContents(r.Context(), owner, repo, targetPath, branch)
	if err != nil {
		if apiErr, isAPI := err.(*githubAPIError); !isAPI || apiErr.StatusCode != http.StatusNotFound {
			writeGitHubUpstreamError(w, err)
			return
		}
	}
	remoteSHA := ""
	if existing != nil && existing.File != nil {
		remoteSHA = existing.File.SHA
	}

	// Conflict: the remote file moved since our last recorded sync.
	link, _ := linkStore.GetGitHubScriptLink(id)
	if link != nil && link.LastCommitSHA != "" && remoteSHA != "" && remoteSHA != link.LastCommitSHA {
		writeError(w, http.StatusConflict, "remote file has changed since the last sync; pull and review before pushing")
		return
	}

	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	if strategy == "" {
		strategy = "commit"
	}

	resp := map[string]any{"strategy": strategy, "owner": owner, "repo": repo, "path": targetPath}
	var writtenBranch string
	var putResult *githubPutResult

	switch strategy {
	case "commit":
		writtenBranch = branch
		putResult, err = client.PutFile(r.Context(), owner, repo, targetPath, branch, message, content, remoteSHA)
		if err != nil {
			writeGitHubUpstreamError(w, err)
			return
		}
	case "pr":
		baseSHA, refErr := client.GetBranchHeadSHA(r.Context(), owner, repo, branch)
		if refErr != nil {
			writeGitHubUpstreamError(w, refErr)
			return
		}
		if baseSHA == "" {
			writeError(w, http.StatusBadRequest, "base branch does not exist")
			return
		}
		prBranch := firstNonEmpty(strings.TrimSpace(req.PRBranch), fmt.Sprintf("apexload/%s-%d", firstNonEmpty(stored.RefKey, stored.ID), time.Now().Unix()))
		if err := client.CreateBranch(r.Context(), owner, repo, prBranch, baseSHA); err != nil {
			// A pre-existing branch is fine; we'll commit onto it.
			if apiErr, isAPI := err.(*githubAPIError); !isAPI || apiErr.StatusCode != http.StatusUnprocessableEntity {
				writeGitHubUpstreamError(w, err)
				return
			}
		}
		writtenBranch = prBranch
		putResult, err = client.PutFile(r.Context(), owner, repo, targetPath, prBranch, message, content, remoteSHA)
		if err != nil {
			writeGitHubUpstreamError(w, err)
			return
		}
		title := firstNonEmpty(strings.TrimSpace(req.PRTitle), message)
		pull, prErr := client.CreatePull(r.Context(), owner, repo, title, prBranch, branch, req.PRBody)
		if prErr != nil {
			writeGitHubUpstreamError(w, prErr)
			return
		}
		resp["pull_request_url"] = pull.HTMLURL
		resp["pull_request_number"] = pull.Number
	default:
		writeError(w, http.StatusBadRequest, "strategy must be 'commit' or 'pr'")
		return
	}

	link = &storage.GitHubScriptLink{
		ScriptID:      id,
		RefKey:        stored.RefKey,
		Owner:         owner,
		Repo:          repo,
		Branch:        writtenBranch,
		Path:          targetPath,
		LastCommitSHA: putResult.Content.SHA,
		ContentHash:   contentHash,
	}
	if err := linkStore.SaveGitHubScriptLink(link); err != nil {
		writeError(w, http.StatusInternalServerError, "push succeeded but the link could not be saved")
		return
	}

	resp["pushed"] = true
	resp["branch"] = writtenBranch
	resp["commit_sha"] = putResult.Commit.SHA
	resp["file_sha"] = putResult.Content.SHA
	resp["html_url"] = githubFileWebURL(settings, owner, repo, writtenBranch, targetPath)
	s.recordAuditEvent(r, "github.script.push", "success", "script", id, stored.ProjectID, map[string]any{
		"owner": owner, "repo": repo, "path": targetPath, "strategy": strategy,
	})
	writeJSON(w, http.StatusOK, resp)
}

// handleScriptGitHubStatus reports the link for a script plus drift: whether the
// local content changed since the last sync (local_drift) and/or the remote file
// changed (remote_drift). Remote checks are skipped gracefully when the caller
// has no GitHub connection.
func (s *Server) handleScriptGitHubStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	linkStore, ok := s.githubScriptLinkStore()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"linked": false})
		return
	}
	link, err := linkStore.GetGitHubScriptLink(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read github link")
		return
	}
	if link == nil {
		writeJSON(w, http.StatusOK, map[string]any{"linked": false, "status": "not_linked"})
		return
	}

	settings := s.currentGitHubSettings()
	_, localHash, err := githubScriptEnvelope(stored.Script)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to serialize script")
		return
	}
	localDrift := link.ContentHash != "" && localHash != link.ContentHash

	out := map[string]any{
		"linked":          true,
		"owner":           link.Owner,
		"repo":            link.Repo,
		"branch":          link.Branch,
		"path":            link.Path,
		"last_commit_sha": link.LastCommitSHA,
		"synced_at":       link.SyncedAt,
		"local_drift":     localDrift,
		"html_url":        githubFileWebURL(settings, link.Owner, link.Repo, link.Branch, link.Path),
	}

	// Best-effort remote check; requires an active connection + enabled feature.
	remoteChecked := false
	remoteDrift := false
	remoteMissing := false
	if settings.Enabled && settings.oauthConfigured() && githubTokenEncryptionAvailable() {
		if store, ok := s.githubConnectionStore(); ok {
			if conn, _ := store.GetGitHubConnection(githubUserKey(r)); conn != nil {
				if token, derr := decryptGitHubToken(conn.AccessTokenEnc); derr == nil && token != "" {
					if client, cerr := newGitHubClient(settings, token, githubClientTimeout); cerr == nil {
						contents, gerr := client.GetContents(r.Context(), link.Owner, link.Repo, link.Path, link.Branch)
						if gerr == nil && contents != nil && contents.File != nil {
							remoteChecked = true
							remoteDrift = contents.File.SHA != link.LastCommitSHA
						} else if apiErr, isAPI := gerr.(*githubAPIError); isAPI && apiErr.StatusCode == http.StatusNotFound {
							remoteChecked = true
							remoteMissing = true
						}
					}
				}
			}
		}
	}
	out["remote_checked"] = remoteChecked
	out["remote_drift"] = remoteDrift
	out["remote_missing"] = remoteMissing
	out["status"] = githubLinkStatus(localDrift, remoteChecked, remoteDrift, remoteMissing)
	writeJSON(w, http.StatusOK, out)
}

func githubLinkStatus(localDrift, remoteChecked, remoteDrift, remoteMissing bool) string {
	switch {
	case remoteChecked && remoteMissing:
		return "remote_missing"
	case localDrift && remoteChecked && remoteDrift:
		return "diverged"
	case localDrift:
		return "local_drift"
	case remoteChecked && remoteDrift:
		return "remote_drift"
	case !remoteChecked:
		return "synced_local"
	default:
		return "synced"
	}
}

// splitOwnerRepo parses an "owner/name" string.
func splitOwnerRepo(full string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(full), "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}
