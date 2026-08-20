package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
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
	store, ok := s.githubConnectionStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github connection storage is unavailable")
		return nil, settings, false
	}
	conn, err := store.GetGitHubConnection(githubUserKey(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read github connection")
		return nil, settings, false
	}
	if conn == nil {
		writeError(w, http.StatusUnauthorized, "github account is not connected")
		return nil, settings, false
	}
	token, ok := s.githubTokenForConnection(r.Context(), settings, conn, store)
	if !ok || strings.TrimSpace(token) == "" {
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

	projectID := normalizeProjectID(req.ProjectID)
	branch := strings.TrimSpace(req.Ref)
	if branch == "" {
		branch = firstNonEmpty(s.githubProjectPolicy(projectID).DefaultBranch, s.currentGitHubSettings().DefaultBranch)
	}
	resolved, ok := s.githubClientForProjectRequest(w, r, projectID, req.Owner, req.Repo)
	if !ok {
		return
	}
	if err := enforceGitHubProjectPolicy(resolved.Policy, "import", req.Owner, req.Repo, branch, req.Path, ""); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	contents, err := resolved.Client.GetContents(r.Context(), req.Owner, req.Repo, req.Path, branch)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	if contents.IsDir || contents.File == nil {
		writeError(w, http.StatusBadRequest, "the selected github path is a directory, not a script file")
		return
	}
	remoteCommitSHA, _ := resolved.Client.GetBranchHeadSHA(r.Context(), req.Owner, req.Repo, branch)
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
	script.ProjectID = projectID

	sourceName := strings.TrimSpace(req.Name)
	if sourceName == "" {
		sourceName = strings.TrimSuffix(path.Base(req.Path), path.Ext(req.Path))
	}
	review := map[string]any{
		"owner":             req.Owner,
		"repo":              req.Repo,
		"branch":            branch,
		"path":              contents.File.Path,
		"commit_sha":        contents.File.SHA,
		"remote_blob_sha":   contents.File.SHA,
		"remote_commit_sha": remoteCommitSHA,
		"html_url":          contents.File.HTMLURL,
		"auth_mode":         resolved.AuthMode,
	}
	draft := importDraft{
		Source:           "github",
		SourceScriptName: sourceName,
		ProjectID:        script.ProjectID,
		Script:           script,
		SourceReview:     review,
		Sections: []scriptImportReviewSection{{
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
	}
	augmented := draft.augment(r)
	warnings := augmented.Warnings

	s.recordAuditEvent(r, "github.script.import", "success", "script", script.Name, script.ProjectID, map[string]any{
		"owner": req.Owner, "repo": req.Repo, "path": contents.File.Path,
	})
	s.writeScriptImportResponseWithSourceSecrets(w, r, http.StatusOK, scriptImportResponseOptions{
		Script:           script,
		Warnings:         warnings,
		Parameterization: buildImportParameterization(script, nil),
		Preflight:        augmented.Preflight,
		Summary: map[string]any{
			"owner":             req.Owner,
			"repo":              req.Repo,
			"branch":            branch,
			"path":              contents.File.Path,
			"commit_sha":        contents.File.SHA,
			"remote_blob_sha":   contents.File.SHA,
			"remote_commit_sha": remoteCommitSHA,
			"auth_mode":         resolved.AuthMode,
		},
		ReviewSections: augmented.Sections,
		SourceReview:   review,
	})
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
//
// The envelope holds the executable definition. A pushed file that had its
// credentials replaced by [REDACTED] could not be imported back into a runnable
// script, and the same bytes are what drift detection compares against the
// remote copy, so masking here also made every credential-bearing script report
// permanent drift.
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
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	if stored.Script == nil || normalizeProjectID(stored.Script.ProjectID) != normalizeProjectID(stored.ProjectID) {
		writeError(w, http.StatusConflict, "script has inconsistent project ownership")
		return
	}

	var req githubPushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	linkStore, ok := s.githubScriptLinkStore()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github link storage is unavailable")
		return
	}
	claimStore, ok := linkStore.(storage.GitHubScriptSyncClaimStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github sync claim storage is unavailable")
		return
	}
	settings := s.currentGitHubSettings()
	policy := s.githubProjectPolicy(stored.ProjectID)

	// Resolve target coordinates, falling back to global defaults.
	owner := strings.TrimSpace(req.Owner)
	repo := strings.TrimSpace(req.Repo)
	if repo == "" {
		// Allow owner/repo packed in DefaultRepo ("owner/name").
		if defOwner, defRepo, ok := splitOwnerRepo(firstNonEmpty(policy.DefaultRepo, settings.DefaultRepo)); ok {
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
	branch := strings.TrimSpace(firstNonEmpty(strings.TrimSpace(req.Branch), policy.DefaultBranch, settings.DefaultBranch))
	targetPath := strings.TrimSpace(firstNonEmpty(strings.TrimSpace(req.Path), renderGitHubPathTemplate(firstNonEmpty(policy.PathTemplate, settings.PathTemplate), stored)))
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

	strategy := strings.ToLower(strings.TrimSpace(req.Strategy))
	if strategy == "" {
		strategy = "commit"
	}
	if policy.Enabled && policy.RequirePullRequest {
		strategy = "pr"
	}
	if strategy != "commit" && strategy != "pr" {
		writeError(w, http.StatusBadRequest, "strategy must be 'commit' or 'pr'")
		return
	}
	if err := enforceGitHubProjectPolicy(policy, "push", owner, repo, branch, targetPath, strategy); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	prBranch := ""
	claimTargets := []storage.GitHubScriptSyncTarget{{Owner: owner, Repo: repo, Branch: branch, Path: targetPath}}
	if strategy == "pr" {
		prBranch = firstNonEmpty(strings.TrimSpace(req.PRBranch), fmt.Sprintf("apexload/%s-%d", firstNonEmpty(stored.RefKey, stored.ID), time.Now().Unix()))
		claimTargets = append(claimTargets, storage.GitHubScriptSyncTarget{Owner: owner, Repo: repo, Branch: prBranch, Path: targetPath})
	}
	claimID := uuid.NewString()
	claimed, err := claimStore.AcquireGitHubScriptSyncClaim(id, claimID, claimTargets, time.Now().UTC().Add(githubScriptSyncClaimTTL))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to acquire github sync claim")
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "another github sync is already running for this script")
		return
	}
	claimHeld := true
	defer func() {
		if claimHeld {
			_, _ = claimStore.ReleaseGitHubScriptSyncClaim(id, claimID)
		}
	}()

	resolved, ok := s.githubClientForProjectRequest(w, r, stored.ProjectID, owner, repo)
	if !ok {
		return
	}
	client := resolved.Client

	// Determine the current remote blob sha (for updates / conflict detection).
	remoteSHA, err := githubSyncRemoteBlobSHA(r.Context(), client, owner, repo, targetPath, branch)
	if err != nil {
		writeGitHubUpstreamError(w, err)
		return
	}
	// Conflict: the remote file moved since our last recorded sync.
	link, err := linkStore.GetGitHubScriptLink(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read github link")
		return
	}
	primaryTarget := githubSyncLinkMatchesTarget(link, owner, repo, branch, targetPath)
	baseTarget := githubSyncBaseWitnessMatchesTarget(link, owner, repo, branch, targetPath)
	witnessSHA, hasWitness := githubSyncDriftWitnessForTarget(link, owner, repo, branch, targetPath)
	if (primaryTarget || baseTarget) && !hasWitness {
		writeError(w, http.StatusConflict, "the stored github drift witness is incomplete; pull and review before pushing")
		return
	}
	if hasWitness && witnessSHA != strings.TrimSpace(remoteSHA) {
		writeError(w, http.StatusConflict, "remote file has changed since the last sync; pull and review before pushing")
		return
	}
	if strategy == "commit" && strings.TrimSpace(remoteSHA) != "" && !hasWitness {
		writeError(w, http.StatusConflict, "remote file already exists without a matching sync witness; pull and reconcile before pushing")
		return
	}
	if strategy == "pr" && link != nil && !primaryTarget && !baseTarget {
		writeError(w, http.StatusConflict, "the script is linked to another github target; reconcile that link before opening a pull request")
		return
	}
	renewed, err := claimStore.RenewGitHubScriptSyncClaim(id, claimID, time.Now().UTC().Add(githubScriptSyncClaimTTL))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to renew github sync claim")
		return
	}
	if !renewed {
		writeError(w, http.StatusConflict, "github sync claim expired before the remote write")
		return
	}

	resp := map[string]any{"strategy": strategy, "owner": owner, "repo": repo, "path": targetPath, "auth_mode": resolved.AuthMode}
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
		if err := client.CreateBranch(r.Context(), owner, repo, prBranch, baseSHA); err != nil {
			// Reusing an existing PR branch would require validating its ancestry and
			// content independently. Fail closed rather than mutating an unvetted head.
			writeGitHubUpstreamError(w, err)
			return
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

	var linkToSave *storage.GitHubScriptLink
	if strategy == "pr" {
		// Link truthfully to the temporary head while retaining the preflighted
		// base as a separate drift witness. This is persisted only after the PR
		// exists, so a failed PR cannot replace the last successful link.
		linkToSave = &storage.GitHubScriptLink{
			ScriptID: id, RefKey: stored.RefKey, Owner: owner, Repo: repo,
			Branch: writtenBranch, Path: targetPath, LastCommitSHA: putResult.Content.SHA,
			RemoteBlobSHA: putResult.Content.SHA, RemoteCommitSHA: putResult.Commit.SHA, ContentHash: contentHash,
			BaseBranch: branch, BaseBlobSHA: remoteSHA, BaseMissing: remoteSHA == "",
		}
	} else {
		linkToSave = &storage.GitHubScriptLink{
			ScriptID: id, RefKey: stored.RefKey, Owner: owner, Repo: repo,
			Branch: writtenBranch, Path: targetPath, LastCommitSHA: putResult.Content.SHA,
			RemoteBlobSHA: putResult.Content.SHA, RemoteCommitSHA: putResult.Commit.SHA, ContentHash: contentHash,
		}
	}
	finalized, err := claimStore.FinalizeGitHubScriptSyncClaim(linkToSave, claimID)
	// Finalize consumes a successful claim. On an indeterminate/failed
	// finalization, leave the durable claim to expire instead of immediately
	// allowing another sync against a remote mutation that may be unrecorded.
	claimHeld = false
	if err != nil {
		writeError(w, http.StatusInternalServerError, "push succeeded but the github sync claim could not be finalized")
		return
	}
	if !finalized {
		writeError(w, http.StatusConflict, "push succeeded but its github sync claim expired; reconcile the remote change before retrying")
		return
	}

	resp["pushed"] = true
	resp["branch"] = writtenBranch
	resp["commit_sha"] = putResult.Commit.SHA
	resp["file_sha"] = putResult.Content.SHA
	resp["remote_blob_sha"] = putResult.Content.SHA
	resp["remote_commit_sha"] = putResult.Commit.SHA
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
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	if stored.Script == nil || normalizeProjectID(stored.Script.ProjectID) != normalizeProjectID(stored.ProjectID) {
		writeError(w, http.StatusConflict, "script has inconsistent project ownership")
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
		"linked":            true,
		"owner":             link.Owner,
		"repo":              link.Repo,
		"branch":            link.Branch,
		"path":              link.Path,
		"last_commit_sha":   link.LastCommitSHA,
		"remote_blob_sha":   link.RemoteBlobSHA,
		"remote_commit_sha": link.RemoteCommitSHA,
		"base_branch":       link.BaseBranch,
		"base_blob_sha":     link.BaseBlobSHA,
		"base_missing":      link.BaseMissing,
		"synced_at":         link.SyncedAt,
		"local_drift":       localDrift,
		"html_url":          githubFileWebURL(settings, link.Owner, link.Repo, link.Branch, link.Path),
	}

	// Best-effort remote check; requires an active connection + enabled feature.
	remoteChecked := false
	remoteDrift := false
	remoteMissing := false
	baseRemoteChecked := false
	baseRemoteDrift := false
	baseRemoteMissing := false
	baseRemoteAttempted := false
	if settings.Enabled && settings.oauthConfigured() && githubTokenEncryptionAvailable() {
		if store, ok := s.githubConnectionStore(); ok {
			if conn, _ := store.GetGitHubConnection(githubUserKey(r)); conn != nil {
				if token, derr := decryptGitHubToken(conn.AccessTokenEnc); derr == nil && token != "" {
					if client, cerr := newGitHubClient(settings, token, githubClientTimeout); cerr == nil {
						remoteChecked, remoteDrift, remoteMissing = githubRemoteWitnessStatus(
							r.Context(), client, link.Owner, link.Repo, link.Path, link.Branch,
							firstNonEmpty(link.RemoteBlobSHA, link.LastCommitSHA), false,
						)
						if strings.TrimSpace(link.BaseBranch) != "" {
							baseRemoteAttempted = true
							baseRemoteChecked, baseRemoteDrift, baseRemoteMissing = githubRemoteWitnessStatus(
								r.Context(), client, link.Owner, link.Repo, link.Path, link.BaseBranch,
								link.BaseBlobSHA, link.BaseMissing,
							)
						}
					}
				}
			}
		}
	}
	out["remote_checked"] = remoteChecked
	out["remote_drift"] = remoteDrift
	out["remote_missing"] = remoteMissing
	out["base_remote_checked"] = baseRemoteChecked
	out["base_remote_drift"] = baseRemoteDrift
	out["base_remote_missing"] = baseRemoteMissing
	out["base_remote_check_failed"] = baseRemoteAttempted && !baseRemoteChecked
	out["status"] = githubLinkStatus(localDrift, remoteChecked, remoteDrift, remoteMissing, baseRemoteAttempted, baseRemoteChecked, baseRemoteDrift)
	writeJSON(w, http.StatusOK, out)
}

func githubRemoteWitnessStatus(ctx context.Context, client *githubClient, owner, repo, path, branch, expectedSHA string, expectedMissing bool) (checked, drift, missing bool) {
	contents, err := client.GetContents(ctx, owner, repo, path, branch)
	if err == nil && contents != nil && contents.File != nil {
		return true, expectedMissing || strings.TrimSpace(contents.File.SHA) != strings.TrimSpace(expectedSHA), false
	}
	if apiErr, isAPI := err.(*githubAPIError); isAPI && apiErr.StatusCode == http.StatusNotFound {
		return true, !expectedMissing, true
	}
	return false, false, false
}

func githubLinkStatus(localDrift, remoteChecked, remoteDrift, remoteMissing, baseRemoteAttempted, baseRemoteChecked, baseRemoteDrift bool) string {
	switch {
	case remoteChecked && remoteMissing:
		return "remote_missing"
	case localDrift && ((remoteChecked && remoteDrift) || (baseRemoteChecked && baseRemoteDrift)):
		return "diverged"
	case localDrift:
		return "local_drift"
	case remoteChecked && remoteDrift:
		return "remote_drift"
	case baseRemoteChecked && baseRemoteDrift:
		return "base_drift"
	case baseRemoteAttempted && !baseRemoteChecked:
		return "partial_remote_check"
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
